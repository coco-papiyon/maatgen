package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestUpstreamStatusAPI(t *testing.T) {
	config := testConfig()
	config.UpstreamStatusReader = func(context.Context) protocol.UpstreamStatus {
		return protocol.UpstreamStatus{
			Config: protocol.UpstreamConfig{Enabled: true, UpstreamURL: "ws://upper:3101/api/relay/connect", NodeID: "linux-dev"},
			State:  protocol.UpstreamStateConnected,
		}
	}
	config.UpstreamConfigSetter = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		t.Fatal("PUT handler should not call the setter for a GET request")
		return protocol.UpstreamStatus{}, nil
	}
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/v1/upstream"))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var status protocol.UpstreamStatus
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.State != protocol.UpstreamStateConnected || status.Config.NodeID != "linux-dev" {
		t.Fatalf("status = %+v", status)
	}
}

func TestUpstreamConfigPutAppliesAndReturnsNewStatus(t *testing.T) {
	var lastConfig protocol.UpstreamConfig
	config := testConfig()
	config.UpstreamStatusReader = func(context.Context) protocol.UpstreamStatus {
		return protocol.UpstreamStatus{State: protocol.UpstreamStateDisabled}
	}
	config.UpstreamConfigSetter = func(_ context.Context, request protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		lastConfig = request
		return protocol.UpstreamStatus{Config: request, State: protocol.UpstreamStateConnecting}, nil
	}
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	body := `{"enabled":true,"upstreamUrl":"ws://upper:3101/api/relay/connect","nodeId":"linux-dev","nodeName":"Linux dev box","nodeToken":"t"}`
	handler.ServeHTTP(recorder, jsonRequest("PUT", "/api/v1/upstream", body))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if lastConfig.NodeID != "linux-dev" || lastConfig.UpstreamURL != "ws://upper:3101/api/relay/connect" {
		t.Fatalf("lastConfig = %+v", lastConfig)
	}
	var status protocol.UpstreamStatus
	if err := json.NewDecoder(recorder.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.State != protocol.UpstreamStateConnecting {
		t.Fatalf("status = %+v", status)
	}
}

func TestUpstreamConfigPutRejectsEnabledWithoutRequiredFields(t *testing.T) {
	config := testConfig()
	config.UpstreamStatusReader = func(context.Context) protocol.UpstreamStatus { return protocol.UpstreamStatus{} }
	config.UpstreamConfigSetter = func(context.Context, protocol.UpstreamConfig) (protocol.UpstreamStatus, error) {
		t.Fatal("setter should not be called when validation fails")
		return protocol.UpstreamStatus{}, nil
	}
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, jsonRequest("PUT", "/api/v1/upstream", `{"enabled":true}`))
	if recorder.Code != 400 {
		t.Fatalf("status = %d, want 400, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpstreamRoutesDisabledWhenReaderOrSetterIsNil(t *testing.T) {
	handler := New(testConfig(), nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, apiRequest("GET", "/api/v1/upstream"))
	if recorder.Code != 404 {
		t.Fatalf("status = %d, want 404 when UpstreamStatusReader/UpstreamConfigSetter are nil", recorder.Code)
	}
}
