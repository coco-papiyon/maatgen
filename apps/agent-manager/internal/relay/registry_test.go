package relay

import (
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/hashicorp/yamux"
)

func TestRegistryCreatePendingThenConnectTransitions(t *testing.T) {
	r := NewRegistry()

	pending, token, err := r.CreatePending("Linux dev box")
	if err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	if pending.Status != protocol.RelayNodeStatusPending {
		t.Fatalf("status = %q, want pending", pending.Status)
	}
	if token == "" {
		t.Fatal("expected a non-empty token")
	}

	session := &yamux.Session{}
	connected := r.Connect(pending.ID, "Linux dev box", session)
	if connected.Status != protocol.RelayNodeStatusConnected {
		t.Fatalf("status = %q, want connected", connected.Status)
	}
	if connected.ConnectedAt == nil {
		t.Fatal("expected ConnectedAt to be set")
	}
	if got, ok := r.Session(pending.ID); !ok || got != session {
		t.Fatalf("Session() = %v, %v; want %v, true", got, ok, session)
	}
}

func TestRegistryConnectWithoutPriorCreateSelfRegisters(t *testing.T) {
	r := NewRegistry()
	session := &yamux.Session{}

	node := r.Connect("linux-dev", "Linux dev box", session)
	if node.Status != protocol.RelayNodeStatusConnected {
		t.Fatalf("status = %q, want connected", node.Status)
	}
	if node.Name != "Linux dev box" {
		t.Fatalf("name = %q, want %q", node.Name, "Linux dev box")
	}

	list := r.List()
	if len(list) != 1 || list[0].ID != "linux-dev" {
		t.Fatalf("List() = %+v, want a single linux-dev entry", list)
	}
}

func TestRegistryDisconnectIgnoresStaleSession(t *testing.T) {
	r := NewRegistry()
	oldSession := &yamux.Session{}
	newSession := &yamux.Session{}

	r.Connect("linux-dev", "Linux dev box", oldSession)
	r.Connect("linux-dev", "Linux dev box", newSession) // a newer connection supersedes the old one

	// The old connection's own goroutine reports its closure after the new
	// one already took over; it must not clobber the current session.
	r.Disconnect("linux-dev", oldSession)

	if _, ok := r.Session("linux-dev"); !ok {
		t.Fatal("expected the current (new) session to still be registered")
	}

	r.Disconnect("linux-dev", newSession)
	if _, ok := r.Session("linux-dev"); ok {
		t.Fatal("expected no session after disconnecting the current one")
	}
	list := r.List()
	if len(list) != 1 || list[0].Status != protocol.RelayNodeStatusDisconnected {
		t.Fatalf("List() = %+v, want a single disconnected entry", list)
	}
}

func TestRegistryDeleteRefusesConnectedNode(t *testing.T) {
	r := NewRegistry()
	session := &yamux.Session{}
	r.Connect("linux-dev", "Linux dev box", session)

	if err := r.Delete("linux-dev"); err != ErrNodeConnected {
		t.Fatalf("Delete() on connected node = %v, want ErrNodeConnected", err)
	}

	r.Disconnect("linux-dev", session)
	if err := r.Delete("linux-dev"); err != nil {
		t.Fatalf("Delete() on disconnected node: %v", err)
	}
	if err := r.Delete("linux-dev"); err != ErrNodeNotFound {
		t.Fatalf("Delete() on already-deleted node = %v, want ErrNodeNotFound", err)
	}
}

func TestRegistryListOmitsNothingAndSortsNewestFirst(t *testing.T) {
	r := NewRegistry()
	first, _, _ := r.CreatePending("first")
	r.now = func() time.Time { return first.CreatedAt.Add(time.Minute) }
	second, _, _ := r.CreatePending("second")

	list := r.List()
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("List() = %+v, want [second, first]", list)
	}
}
