package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func nestedSessionHandler(session protocol.AgentSession) http.Handler {
	reader := &fakeSessionReader{sessions: []protocol.AgentSession{session}}
	return New(testConfig(), reader, nil).Handler()
}

func TestAggregateSessionsMergesLocalConnectedAndUpstreamNodes(t *testing.T) {
	local := protocol.AgentSession{ID: "session-local", Status: protocol.SessionActive, CreatedAt: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)}
	lower := protocol.AgentSession{ID: "session-lower", Status: protocol.SessionActive, CreatedAt: time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)}
	upper := protocol.AgentSession{ID: "session-upper", Status: protocol.SessionActive, CreatedAt: time.Date(2026, 8, 15, 14, 0, 0, 0, time.UTC)}

	config := testConfig()
	config.RelayController = &fakeRelayController{
		nodes:        []protocol.RelayNode{{ID: "linux-dev", Name: "Linux dev box", Status: protocol.RelayNodeStatusConnected}},
		proxyOK:      true,
		proxyHandler: nestedSessionHandler(lower),
	}
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus {
		return []protocol.UpstreamStatus{{Config: protocol.UpstreamConfig{ID: "u1", NodeName: "Office Server"}, State: protocol.UpstreamStateConnected}}
	}
	config.UpstreamProxyProvider = func(id string) (http.Handler, bool) {
		if id != "u1" {
			return nil, false
		}
		return nestedSessionHandler(upper), true
	}

	reader := &fakeSessionReader{sessions: []protocol.AgentSession{local}}
	handler := New(config, reader, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/sessions/all"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.NodeScopedSessionListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Sessions) != 3 {
		t.Fatalf("sessions = %+v, want 3", response.Sessions)
	}
	// Newest createdAt first: upper, local, lower.
	if response.Sessions[0].ID != "session-upper" || response.Sessions[0].NodeID != "upstream:u1" || response.Sessions[0].NodeName != "Office Server" {
		t.Fatalf("sessions[0] = %+v", response.Sessions[0])
	}
	if response.Sessions[1].ID != "session-local" || response.Sessions[1].NodeID != "local" || response.Sessions[1].NodeName != "Local" {
		t.Fatalf("sessions[1] = %+v", response.Sessions[1])
	}
	if response.Sessions[2].ID != "session-lower" || response.Sessions[2].NodeID != "linux-dev" || response.Sessions[2].NodeName != "Linux dev box" {
		t.Fatalf("sessions[2] = %+v", response.Sessions[2])
	}
	if len(response.UnavailableNodes) != 0 {
		t.Fatalf("unavailableNodes = %+v, want none", response.UnavailableNodes)
	}
}

func TestAggregateSessionsTreatsDisconnectedOrPendingNodesAsUnreachable(t *testing.T) {
	config := testConfig()
	config.RelayController = &fakeRelayController{
		nodes: []protocol.RelayNode{
			{ID: "pending-node", Name: "Pending", Status: protocol.RelayNodeStatusPending},
			{ID: "gone-node", Name: "Gone", Status: protocol.RelayNodeStatusDisconnected},
		},
	}
	reader := &fakeSessionReader{sessions: []protocol.AgentSession{{ID: "session-local", Status: protocol.SessionActive, CreatedAt: time.Now()}}}
	handler := New(config, reader, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/sessions/all"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.NodeScopedSessionListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Sessions) != 1 || response.Sessions[0].NodeID != "local" {
		t.Fatalf("sessions = %+v, want only the local session (pending/disconnected nodes are skipped, not queried)", response.Sessions)
	}
}

func TestAggregateSessionsReportsUnavailableNodeWithoutFailingTheWholeRequest(t *testing.T) {
	config := testConfig()
	config.RelayController = &fakeRelayController{
		nodes:   []protocol.RelayNode{{ID: "flaky-node", Name: "Flaky", Status: protocol.RelayNodeStatusConnected}},
		proxyOK: true,
		proxyHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	}
	reader := &fakeSessionReader{sessions: []protocol.AgentSession{{ID: "session-local", Status: protocol.SessionActive, CreatedAt: time.Now()}}}
	handler := New(config, reader, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/sessions/all"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.NodeScopedSessionListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Sessions) != 1 || response.Sessions[0].NodeID != "local" {
		t.Fatalf("sessions = %+v, want only the local session", response.Sessions)
	}
	if len(response.UnavailableNodes) != 1 || response.UnavailableNodes[0] != "flaky-node" {
		t.Fatalf("unavailableNodes = %+v, want [flaky-node]", response.UnavailableNodes)
	}
}

func TestAggregateSessionsForwardsStatusFilterAndLimit(t *testing.T) {
	config := testConfig()
	reader := &fakeSessionReader{sessions: []protocol.AgentSession{}}
	handler := New(config, reader, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/sessions/all?status=closed&limit=5"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if reader.status != protocol.SessionClosed {
		t.Fatalf("status filter = %q, want closed", reader.status)
	}
	if reader.limit != 5 {
		t.Fatalf("limit = %d, want 5", reader.limit)
	}
}

func TestAggregateSessionsRouteDisabledWhenSessionsNil(t *testing.T) {
	handler := New(testConfig(), nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/sessions/all"))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when SessionReader is nil", recorder.Code)
	}
}
