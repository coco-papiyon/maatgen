package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/relay"
)

type fakeRelayController struct {
	nodes        []protocol.RelayNode
	createResult protocol.RelayNode
	createErr    error
	deleteErr    error
	proxyHandler http.Handler
	proxyOK      bool

	lastCreateName string
	lastDeleteID   string
	lastProxyID    string
}

func (f *fakeRelayController) ListNodes(context.Context) []protocol.RelayNode { return f.nodes }

func (f *fakeRelayController) CreateNode(_ context.Context, name string) (protocol.RelayNode, error) {
	f.lastCreateName = name
	return f.createResult, f.createErr
}

func (f *fakeRelayController) DeleteNode(_ context.Context, id string) error {
	f.lastDeleteID = id
	return f.deleteErr
}

func (f *fakeRelayController) NodeProxy(id string) (http.Handler, bool) {
	f.lastProxyID = id
	return f.proxyHandler, f.proxyOK
}

func TestRelayListNodesAlwaysIncludesLocalFirst(t *testing.T) {
	controller := &fakeRelayController{nodes: []protocol.RelayNode{
		{ID: "linux-dev", Name: "Linux dev box", Status: protocol.RelayNodeStatusConnected},
	}}
	config := testConfig()
	config.RelayController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/nodes"))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.RelayNodeListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Nodes) != 2 || response.Nodes[0].ID != "local" || response.Nodes[1].ID != "linux-dev" {
		t.Fatalf("nodes = %+v, want [local, linux-dev]", response.Nodes)
	}
}

func TestRelayCreateNodeAPI(t *testing.T) {
	controller := &fakeRelayController{createResult: protocol.RelayNode{
		ID: "node-abc", Name: "Linux dev box", Status: protocol.RelayNodeStatusPending, StartupCommand: "agent-manager --upstream-url ...",
	}}
	config := testConfig()
	config.RelayController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, jsonRequest("POST", "/api/nodes", `{"name":"Linux dev box"}`))
	if recorder.Code != 201 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if controller.lastCreateName != "Linux dev box" {
		t.Fatalf("lastCreateName = %q", controller.lastCreateName)
	}
	var node protocol.RelayNode
	if err := json.NewDecoder(recorder.Body).Decode(&node); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if node.StartupCommand == "" {
		t.Fatal("expected a non-empty startup command in the response")
	}
}

func TestRelayDeleteNodeErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", relay.ErrNodeNotFound, 404},
		{"connected", relay.ErrNodeConnected, 409},
		{"ok", nil, 204},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			controller := &fakeRelayController{deleteErr: tc.err}
			config := testConfig()
			config.RelayController = controller
			handler := New(config, nil, nil).Handler()

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, apiRequest("DELETE", "/api/nodes/node-abc"))
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, tc.want, recorder.Body.String())
			}
			if controller.lastDeleteID != "node-abc" {
				t.Fatalf("lastDeleteID = %q", controller.lastDeleteID)
			}
		})
	}
}

func TestRelayDeleteLocalNodeRejected(t *testing.T) {
	controller := &fakeRelayController{}
	config := testConfig()
	config.RelayController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("DELETE", "/api/nodes/local"))
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if controller.lastDeleteID != "" {
		t.Fatal("expected DeleteNode not to be called for the local node")
	}
}

func TestRelayProxyStripsNodePrefixAndForwardsRemainder(t *testing.T) {
	var gotPath, gotQuery string
	controller := &fakeRelayController{
		proxyOK: true,
		proxyHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotQuery = r.URL.RawQuery
			w.WriteHeader(http.StatusTeapot)
		}),
	}
	config := testConfig()
	config.RelayController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/nodes/linux-dev/api/v1/sessions?status=active"))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTeapot)
	}
	if controller.lastProxyID != "linux-dev" {
		t.Fatalf("lastProxyID = %q", controller.lastProxyID)
	}
	if gotPath != "/api/v1/sessions" {
		t.Fatalf("forwarded path = %q, want /api/v1/sessions", gotPath)
	}
	if gotQuery != "status=active" {
		t.Fatalf("forwarded query = %q, want status=active", gotQuery)
	}
}

func TestRelayProxyReturnsNotFoundWhenNodeNotConnected(t *testing.T) {
	controller := &fakeRelayController{proxyOK: false}
	config := testConfig()
	config.RelayController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/nodes/linux-dev/api/v1/sessions"))
	if recorder.Code != 404 {
		t.Fatalf("status = %d, want 404, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestRelayRoutesDisabledWhenControllerNil(t *testing.T) {
	handler := New(testConfig(), nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/nodes"))
	if recorder.Code != 404 {
		t.Fatalf("status = %d, want 404 when RelayController is nil", recorder.Code)
	}
}
