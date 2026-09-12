package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestUpstreamListAPI(t *testing.T) {
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus {
		return []protocol.UpstreamStatus{{
			Config: protocol.UpstreamConfig{ID: "u1", Enabled: true, UpstreamURL: "ws://upper:3101/api/relay/connect", NodeID: "linux-dev"},
			State:  protocol.UpstreamStateConnected,
		}}
	}
	config.UpstreamCreator = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		t.Fatal("GET handler should not call the creator")
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamUpdater = func(context.Context, string, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		t.Fatal("GET handler should not call the updater")
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamDeleter = func(context.Context, string) error {
		t.Fatal("GET handler should not call the deleter")
		return nil
	}
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/v1/upstreams"))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.UpstreamStatusListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Upstreams) != 1 || response.Upstreams[0].State != protocol.UpstreamStateConnected || response.Upstreams[0].Config.NodeID != "linux-dev" {
		t.Fatalf("upstreams = %+v", response.Upstreams)
	}
}

func TestUpstreamCreatePostAppliesAndReturnsNewStatus(t *testing.T) {
	var lastConfig protocol.UpstreamConfig
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus { return nil }
	config.UpstreamCreator = func(_ context.Context, request protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		lastConfig = request
		request.ID = "u1"
		return protocol.UpstreamStatus{Config: request, State: protocol.UpstreamStateConnecting}, nil
	}
	config.UpstreamUpdater = func(context.Context, string, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		t.Fatal("POST handler should not call the updater")
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamDeleter = func(context.Context, string) error { return nil }
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	body := `{"enabled":true,"upstreamUrl":"ws://upper:3101/api/relay/connect","nodeId":"linux-dev","nodeName":"Linux dev box","nodeToken":"t"}`
	handler.ServeHTTP(recorder, jsonRequest("POST", "/api/v1/upstreams", body))
	if recorder.Code != 201 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if lastConfig.NodeID != "linux-dev" || lastConfig.UpstreamURL != "ws://upper:3101/api/relay/connect" {
		t.Fatalf("lastConfig = %+v", lastConfig)
	}
	var status protocol.UpstreamStatus
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.State != protocol.UpstreamStateConnecting || status.Config.ID != "u1" {
		t.Fatalf("status = %+v", status)
	}
}

func TestUpstreamUpdatePutAppliesAndReturnsNewStatus(t *testing.T) {
	var lastID string
	var lastConfig protocol.UpstreamConfig
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus { return nil }
	config.UpstreamCreator = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		t.Fatal("PUT handler should not call the creator")
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamUpdater = func(_ context.Context, id string, request protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		lastID = id
		lastConfig = request
		request.ID = id
		return protocol.UpstreamStatus{Config: request, State: protocol.UpstreamStateConnecting}, nil
	}
	config.UpstreamDeleter = func(context.Context, string) error { return nil }
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	body := `{"enabled":true,"upstreamUrl":"ws://upper:3101/api/relay/connect","nodeId":"linux-dev","nodeName":"Linux dev box","nodeToken":"t"}`
	handler.ServeHTTP(recorder, jsonRequest("PUT", "/api/v1/upstreams/u1", body))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if lastID != "u1" || lastConfig.NodeID != "linux-dev" {
		t.Fatalf("lastID = %q, lastConfig = %+v", lastID, lastConfig)
	}
	var status protocol.UpstreamStatus
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.State != protocol.UpstreamStateConnecting {
		t.Fatalf("status = %+v", status)
	}
}

func TestUpstreamUpdatePutRejectsEnabledWithoutRequiredFields(t *testing.T) {
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus { return nil }
	config.UpstreamCreator = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamUpdater = func(context.Context, string, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		t.Fatal("setter should not be called when validation fails")
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamDeleter = func(context.Context, string) error { return nil }
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, jsonRequest("PUT", "/api/v1/upstreams/u1", `{"enabled":true}`))
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpstreamUpdatePutReturns404ForUnknownID(t *testing.T) {
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus { return nil }
	config.UpstreamCreator = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamUpdater = func(context.Context, string, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, ErrUpstreamNotFound
	}
	config.UpstreamDeleter = func(context.Context, string) error { return nil }
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, jsonRequest("PUT", "/api/v1/upstreams/missing", `{"enabled":false}`))
	if recorder.Code != 404 {
		t.Fatalf("status = %d, want 404, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpstreamDeleteRemovesEntry(t *testing.T) {
	var deletedID string
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus { return nil }
	config.UpstreamCreator = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamUpdater = func(context.Context, string, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamDeleter = func(_ context.Context, id string) error {
		deletedID = id
		return nil
	}
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("DELETE", "/api/v1/upstreams/u1"))
	if recorder.Code != 204 {
		t.Fatalf("status = %d, want 204, body = %s", recorder.Code, recorder.Body.String())
	}
	if deletedID != "u1" {
		t.Fatalf("deletedID = %q, want u1", deletedID)
	}
}

func TestUpstreamDeleteReturns404ForUnknownID(t *testing.T) {
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus { return nil }
	config.UpstreamCreator = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamUpdater = func(context.Context, string, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamDeleter = func(context.Context, string) error { return ErrUpstreamNotFound }
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("DELETE", "/api/v1/upstreams/missing"))
	if recorder.Code != 404 {
		t.Fatalf("status = %d, want 404, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpstreamRoutesDisabledWhenAnyControllerFuncIsNil(t *testing.T) {
	handler := New(testConfig(), nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/v1/upstreams"))
	if recorder.Code != 404 {
		t.Fatalf("status = %d, want 404 when Upstream* controller functions are nil", recorder.Code)
	}
}

func TestUpstreamProxyStripsPrefix(t *testing.T) {
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus { return nil }
	config.UpstreamCreator = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamUpdater = func(context.Context, string, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamDeleter = func(context.Context, string) error { return nil }
	config.UpstreamProxyProvider = func(id string) (http.Handler, bool) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(id + ":" + r.URL.Path))
		}), true
	}

	recorder := httptest.NewRecorder()
	New(config, nil, nil).Handler().ServeHTTP(recorder, apiRequest("GET", "/api/upstreams/u1/api/v1/sessions"))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "u1:/api/v1/sessions" {
		t.Fatalf("proxy response = status %d body %q", recorder.Code, recorder.Body.String())
	}
}

func TestUpstreamProxyReturnsUnavailableWhenDisconnected(t *testing.T) {
	config := testConfig()
	config.UpstreamLister = func(context.Context) []protocol.UpstreamStatus { return nil }
	config.UpstreamCreator = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamUpdater = func(context.Context, string, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		return protocol.UpstreamStatus{}, nil
	}
	config.UpstreamDeleter = func(context.Context, string) error { return nil }
	config.UpstreamProxyProvider = func(id string) (http.Handler, bool) { return nil, false }

	recorder := httptest.NewRecorder()
	New(config, nil, nil).Handler().ServeHTTP(recorder, apiRequest("GET", "/api/upstreams/u1/api/v1/sessions"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}
