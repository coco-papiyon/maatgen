package server

import (
	"testing"
	"time"
)

func TestTicketIsSingleUseAndExpires(t *testing.T) {
	current := time.Date(2026, 8, 15, 5, 0, 0, 0, time.UTC)
	store := newTicketStore(30 * time.Second)
	store.now = func() time.Time { return current }

	ticket, _, err := store.issue()
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	if !store.consume(ticket) {
		t.Fatal("fresh ticket was rejected")
	}
	if store.consume(ticket) {
		t.Fatal("ticket was accepted twice")
	}

	expired, _, err := store.issue()
	if err != nil {
		t.Fatalf("issue expiring ticket: %v", err)
	}
	current = current.Add(31 * time.Second)
	if store.consume(expired) {
		t.Fatal("expired ticket was accepted")
	}
}
