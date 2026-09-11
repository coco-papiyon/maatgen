package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// TestConnectAndProxyRoundTrip exercises the whole ADR-009 mechanism
// end-to-end: a lower node dials an upper node's /api/relay/connect over a
// real WebSocket connection, the upper node multiplexes it with yamux and
// registers it, and a request built by NewReverseProxy reaches the lower
// node's own, completely ordinary http.Handler unchanged — proving there is
// no relay-specific request/response envelope on the wire (Decision 2).
func TestConnectAndProxyRoundTrip(t *testing.T) {
	registry := NewRegistry()
	upper := httptest.NewServer(ConnectHandler(registry, nil))
	defer upper.Close()
	upstreamURL := "ws" + upper.URL[len("http"):] + "/api/relay/connect"

	lowerHandler := http.NewServeMux()
	lowerHandler.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from " + r.URL.Query().Get("who")))
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunClient(ctx, ClientOptions{
		UpstreamURL: upstreamURL,
		NodeID:      "linux-dev",
		NodeName:    "Linux dev box",
		Handler:     lowerHandler,
	})

	session := waitForSession(t, registry, "linux-dev")

	list := registry.List()
	if len(list) != 1 || list[0].Name != "Linux dev box" {
		t.Fatalf("List() = %+v, want a single connected \"Linux dev box\" entry", list)
	}

	proxy := NewReverseProxy(session)
	req := httptest.NewRequest(http.MethodGet, "/hello?who=lower+node", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Test"); got != "ok" {
		t.Fatalf("X-Test header = %q, want ok", got)
	}
	if got := rec.Body.String(); got != "hello from lower node" {
		t.Fatalf("body = %q", got)
	}
}

func TestConnectedLowerNodeCanProxyBackToUpper(t *testing.T) {
	upperHandler := http.NewServeMux()
	upperHandler.HandleFunc("/upper", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello from upper"))
	})
	service := NewService("", nil)
	service.SetHandler(upperHandler)
	upper := httptest.NewServer(service.ConnectHandler())
	defer upper.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	connected := make(chan *yamux.Session, 1)
	go RunClient(ctx, ClientOptions{
		UpstreamURL: "ws" + upper.URL[len("http"):] + "/api/relay/connect",
		NodeID:      "linux-dev",
		NodeName:    "Linux dev box",
		Handler:     http.NewServeMux(),
		OnConnected: func(session *yamux.Session) { connected <- session },
	})

	var session *yamux.Session
	select {
	case session = <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lower node connection")
	}

	proxy := NewReverseProxy(session)
	req := httptest.NewRequest(http.MethodGet, "/upper", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "hello from upper" {
		t.Fatalf("reverse request = status %d body %q, want 200 and upper response", rec.Code, rec.Body.String())
	}
}

// TestConnectMarksNodeDisconnectedWhenLowerNodeGoesAway checks the other
// half of the lifecycle: once the lower node's connection drops, the
// registry reflects it without anyone polling — ConnectHandler's own
// goroutine notices the session close.
func TestConnectMarksNodeDisconnectedWhenLowerNodeGoesAway(t *testing.T) {
	registry := NewRegistry()
	upper := httptest.NewServer(ConnectHandler(registry, nil))
	defer upper.Close()
	upstreamURL := "ws" + upper.URL[len("http"):] + "/api/relay/connect"

	ctx, cancel := context.WithCancel(context.Background())
	go RunClient(ctx, ClientOptions{
		UpstreamURL: upstreamURL,
		NodeID:      "linux-dev",
		NodeName:    "Linux dev box",
		Handler:     http.NewServeMux(),
	})

	waitForSession(t, registry, "linux-dev")
	cancel() // stop the lower node without waiting for another connection attempt

	deadline := time.Now().Add(5 * time.Second)
	for {
		list := registry.List()
		if len(list) == 1 && list[0].Status == "disconnected" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for node to be marked disconnected; last state: %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForSession(t *testing.T, registry *Registry, nodeID string) *yamux.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if session, ok := registry.Session(nodeID); ok {
			return session
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for node %q to connect", nodeID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
