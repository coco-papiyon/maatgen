package relay

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// fakeClock advances retry waits without wall-clock delays.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Waits() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.waits = append(c.waits, d)
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

func TestClientSupervisorStopsAfterConfiguredFailuresAndReconnects(t *testing.T) {
	supervisor, clock := newTestSupervisor(http.NewServeMux())
	supervisor.SetRetrySettings(protocol.UpstreamRetrySettings{MaxFailures: 2, RetryIntervalMinutes: 7})
	config := protocol.UpstreamConfig{Enabled: true, UpstreamURL: "ws://127.0.0.1:1/api/relay/connect", NodeID: "n1"}
	supervisor.Set("u1", config)

	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := supervisor.Get("u1")
		if status.State == protocol.UpstreamStateStopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for stopped state; last status: %+v", status)
		}
		time.Sleep(time.Millisecond)
	}

	status, _ := supervisor.Get("u1")
	if status.FailureCount != 2 || status.NextAttemptAt != nil {
		t.Fatalf("stopped status = %+v", status)
	}
	if waits := clock.Waits(); len(waits) != 1 || waits[0] != 7*time.Minute {
		t.Fatalf("retry waits = %v, want one 7-minute wait", waits)
	}
	if status.LastError == "" {
		t.Fatal("expected a LastError describing the failed connection attempt")
	}
	if _, ok := supervisor.UpstreamProxy("u1"); ok {
		t.Fatal("stopped upstream must not have a proxy")
	}
	if restarted, ok := supervisor.Reconnect("u1"); !ok || restarted.State != protocol.UpstreamStateConnecting || restarted.FailureCount != 0 {
		t.Fatalf("reconnect status = %+v, ok = %t", restarted, ok)
	}
	// The saved configuration remains enabled, so startup can start it again.
	supervisor.Set("u1", config)
	if restarted, ok := supervisor.Get("u1"); !ok || restarted.State != protocol.UpstreamStateConnecting {
		t.Fatalf("startup status = %+v, ok = %t", restarted, ok)
	}
}

func TestClientSupervisorDefaultsToThreeFailures(t *testing.T) {
	supervisor, clock := newTestSupervisor(http.NewServeMux())
	supervisor.Set("u1", protocol.UpstreamConfig{Enabled: true, UpstreamURL: "ws://127.0.0.1:1/api/relay/connect", NodeID: "n1"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := supervisor.Get("u1")
		if status.State == protocol.UpstreamStateStopped {
			if status.FailureCount != 3 {
				t.Fatalf("failure count = %d, want 3", status.FailureCount)
			}
			if waits := clock.Waits(); len(waits) != 2 || waits[0] != 5*time.Minute || waits[1] != 5*time.Minute {
				t.Fatalf("retry waits = %v, want two 5-minute waits", waits)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for default limit; status = %+v", status)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestClientSupervisorReschedulesWaitingConnectionWhenCommonIntervalChanges(t *testing.T) {
	supervisor := NewClientSupervisor(http.NewServeMux(), nil)
	waits := make(chan time.Duration, 2)
	supervisor.after = func(duration time.Duration) <-chan time.Time {
		waits <- duration
		return make(chan time.Time)
	}
	supervisor.Set("u1", protocol.UpstreamConfig{Enabled: true, UpstreamURL: "ws://127.0.0.1:1/api/relay/connect", NodeID: "n1"})
	select {
	case first := <-waits:
		if first < 4*time.Minute || first > 5*time.Minute {
			t.Fatalf("first wait = %s, want about 5 minutes", first)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first retry was not scheduled")
	}
	supervisor.SetRetrySettings(protocol.UpstreamRetrySettings{MaxFailures: 4, RetryIntervalMinutes: 7})
	select {
	case second := <-waits:
		if second < 6*time.Minute || second > 7*time.Minute {
			t.Fatalf("updated wait = %s, want about 7 minutes", second)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("updated retry was not scheduled")
	}
	supervisor.Close()
}
