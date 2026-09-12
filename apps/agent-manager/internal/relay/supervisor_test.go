package relay

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// fakeClock lets a test compress the multi-minute quiet-interval escalation
// (supervisorQuietAfter/supervisorQuietInterval) into real time on the
// order of milliseconds: ClientSupervisor.after advances it by exactly the
// duration it was asked to wait, then fires immediately.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}

func newTestSupervisor(handler http.Handler) (*ClientSupervisor, *fakeClock) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	supervisor := NewClientSupervisor(handler, nil)
	supervisor.now = clock.Now
	supervisor.after = clock.After
	return supervisor, clock
}

func TestClientSupervisorDisabledConfigDoesNotConnect(t *testing.T) {
	supervisor, _ := newTestSupervisor(http.NewServeMux())
	supervisor.Set("u1", protocol.UpstreamConfig{Enabled: false, UpstreamURL: "ws://127.0.0.1:1/api/relay/connect", NodeID: "n1"})

	status, ok := supervisor.Get("u1")
	if !ok || status.State != protocol.UpstreamStateDisabled {
		t.Fatalf("state = %q, want disabled", status.State)
	}
}

func TestClientSupervisorConnectsAndReportsStatus(t *testing.T) {
	registry := NewRegistry()
	upper := httptest.NewServer(ConnectHandler(registry, nil))
	defer upper.Close()
	upstreamURL := "ws" + upper.URL[len("http"):] + "/api/relay/connect"

	supervisor, _ := newTestSupervisor(http.NewServeMux())
	supervisor.Set("u1", protocol.UpstreamConfig{Enabled: true, UpstreamURL: upstreamURL, NodeID: "linux-dev", NodeName: "Linux dev box"})

	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := supervisor.Get("u1")
		if status.State == protocol.UpstreamStateConnected {
			if status.LastConnectedAt == nil {
				t.Fatal("expected LastConnectedAt to be set once connected")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for connected state; last status: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := supervisor.UpstreamProxy("u1"); !ok {
		t.Fatal("expected an upstream proxy while connected")
	}

	// Reconfiguring away should stop the connection and drop it from the registry.
	supervisor.Set("u1", protocol.UpstreamConfig{Enabled: false})
	if _, ok := supervisor.UpstreamProxy("u1"); ok {
		t.Fatal("upstream proxy remained available after disabling")
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if len(registry.List()) == 0 || registry.List()[0].Status == protocol.RelayNodeStatusDisconnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for upstream registry entry to disconnect: %+v", registry.List())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status, ok := supervisor.Get("u1"); !ok || status.State != protocol.UpstreamStateDisabled {
		t.Fatalf("state after disabling = %q, want disabled", status.State)
	}
}

func TestClientSupervisorRunsMultipleConnectionsIndependently(t *testing.T) {
	registryA := NewRegistry()
	upperA := httptest.NewServer(ConnectHandler(registryA, nil))
	defer upperA.Close()
	registryB := NewRegistry()
	upperB := httptest.NewServer(ConnectHandler(registryB, nil))
	defer upperB.Close()

	supervisor, _ := newTestSupervisor(http.NewServeMux())
	supervisor.Set("a", protocol.UpstreamConfig{Enabled: true, UpstreamURL: "ws" + upperA.URL[len("http"):] + "/api/relay/connect", NodeID: "node-a"})
	supervisor.Set("b", protocol.UpstreamConfig{Enabled: true, UpstreamURL: "ws" + upperB.URL[len("http"):] + "/api/relay/connect", NodeID: "node-b"})

	deadline := time.Now().Add(5 * time.Second)
	for {
		statusA, _ := supervisor.Get("a")
		statusB, _ := supervisor.Get("b")
		if statusA.State == protocol.UpstreamStateConnected && statusB.State == protocol.UpstreamStateConnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for both connections; a=%+v b=%+v", statusA, statusB)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Removing "a" must not disturb "b"'s live connection.
	supervisor.Remove("a")
	if _, ok := supervisor.Get("a"); ok {
		t.Fatal("expected \"a\" to be forgotten after Remove")
	}
	if statusB, ok := supervisor.Get("b"); !ok || statusB.State != protocol.UpstreamStateConnected {
		t.Fatalf("removing \"a\" affected \"b\": %+v", statusB)
	}
	if _, ok := supervisor.UpstreamProxy("b"); !ok {
		t.Fatal("expected \"b\" to still have a proxy after removing \"a\"")
	}
}

// TestClientSupervisorBacksOffToQuietIntervalAfterSustainedFailure exercises
// the "don't keep checking a long-offline upper node every few seconds"
// requirement: with a target nothing is listening on, wait durations must
// grow (capped at supervisorFastMaxBackoff) and then, once
// supervisorQuietAfter of simulated failure has passed, jump to
// supervisorQuietInterval and report UpstreamStateWaiting.
func TestClientSupervisorBacksOffToQuietIntervalAfterSustainedFailure(t *testing.T) {
	supervisor, _ := newTestSupervisor(http.NewServeMux())
	// Port 1 on loopback refuses immediately, so dialOnce fails fast without
	// a real timeout — the fake clock (not wall time) drives the backoff.
	supervisor.Set("u1", protocol.UpstreamConfig{Enabled: true, UpstreamURL: "ws://127.0.0.1:1/api/relay/connect", NodeID: "n1"})

	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := supervisor.Get("u1")
		if status.State == protocol.UpstreamStateWaiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the quiet backoff state; last status: %+v", status)
		}
		time.Sleep(time.Millisecond)
	}

	status, _ := supervisor.Get("u1")
	if status.NextAttemptAt == nil {
		t.Fatal("expected NextAttemptAt to be set")
	}
	if status.LastError == "" {
		t.Fatal("expected a LastError describing the failed connection attempt")
	}
}
