package server

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

type wsTicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type ticketStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	tickets map[string]time.Time
}

func newTicketStore(ttl time.Duration) *ticketStore {
	return &ticketStore{ttl: ttl, now: time.Now, tickets: make(map[string]time.Time)}
}

func (s *ticketStore) issue() (string, time.Time, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", time.Time{}, err
	}
	ticket := base64.RawURLEncoding.EncodeToString(bytes)
	expiresAt := s.now().UTC().Add(s.ttl)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked()
	s.tickets[ticket] = expiresAt
	return ticket, expiresAt, nil
}

func (s *ticketStore) consume(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked()
	expiresAt, ok := s.tickets[ticket]
	if !ok || !expiresAt.After(s.now().UTC()) {
		delete(s.tickets, ticket)
		return false
	}
	delete(s.tickets, ticket)
	return true
}

func (s *ticketStore) removeExpiredLocked() {
	now := s.now().UTC()
	for ticket, expiresAt := range s.tickets {
		if !expiresAt.After(now) {
			delete(s.tickets, ticket)
		}
	}
}
