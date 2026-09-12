package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/hashicorp/yamux"
)

const (
	supervisorInitialBackoff = time.Second
	supervisorFastMaxBackoff = 30 * time.Second
	// supervisorQuietAfter/supervisorQuietInterval: once the upper node has
	// been unreachable continuously for this long, stop retrying every few
	// seconds and fall back to one attempt every supervisorQuietInterval
	// instead, so a lower node left running against an upper node that is
	// simply off (e.g. overnight) does not keep reconnecting/logging in a
	// tight loop.
	supervisorQuietAfter    = 2 * time.Minute
	supervisorQuietInterval = 5 * time.Minute
)

// supervisorEntry is one configured outbound connection (one upper node).
// run() holds a direct pointer to its own entry rather than looking it up
// in ClientSupervisor.entries by id each time, so that once Set or Remove
// has replaced/deleted the map entry for an id, the superseded goroutine's
// ctx.Err() check (in update) stops it from writing into a status that no
// longer represents the current configuration for that id.
type supervisorEntry struct {
	cancel  context.CancelFunc
	session *yamux.Session
	status  protocol.UpstreamStatus
}

// ClientSupervisor runs this (lower) node's outbound relay connections to
// one or more upper nodes, and lets each target be added, edited, or
// removed at runtime — from a "Server" settings screen, not just
// --upstream-url at startup — without restarting the process or disturbing
// the other configured connections. Set replaces (or creates) exactly the
// entry for the given id; every other id's connection is left alone.
type ClientSupervisor struct {
	handler http.Handler
	logger  *slog.Logger
	// now and after are overridden by tests with a fake clock so the
	// multi-minute quiet-interval escalation can be exercised without a
	// real multi-minute wait.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu      sync.Mutex
	entries map[string]*supervisorEntry
}

func NewClientSupervisor(handler http.Handler, logger *slog.Logger) *ClientSupervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClientSupervisor{
		handler: handler,
		logger:  logger,
		now:     time.Now,
		after:   time.After,
		entries: make(map[string]*supervisorEntry),
	}
}

// SetHandler sets the handler served over every relay session once it
// becomes available. It exists because the handler (server.New(...).
// Handler()) commonly depends on a server.Config whose upstream fields
// close over this same ClientSupervisor — callers construct the supervisor
// with a nil handler, build that config, then call SetHandler once the
// handler exists, before the first Set.
func (s *ClientSupervisor) SetHandler(handler http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = handler
}

// GenerateID returns a random id for a newly created upstream entry.
func GenerateID() (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "upstream-" + hex.EncodeToString(random), nil
}

// Set replaces (or creates) the connection identified by id with config,
// stopping whatever connection attempt loop previously ran for that id (if
// any) before starting a new one, so there is never more than one live
// attempt per id. Disabling (Enabled: false) or leaving UpstreamURL/NodeID
// empty stops any running connection for id without starting a new one,
// but keeps the (disabled) entry so it still shows up in List.
func (s *ClientSupervisor) Set(id string, config protocol.UpstreamConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[id]; ok && existing.cancel != nil {
		existing.cancel()
	}
	entry := &supervisorEntry{}
	s.entries[id] = entry
	if !config.Enabled || config.UpstreamURL == "" || config.NodeID == "" {
		entry.status = protocol.UpstreamStatus{Config: config, State: protocol.UpstreamStateDisabled}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	entry.cancel = cancel
	entry.status = protocol.UpstreamStatus{Config: config, State: protocol.UpstreamStateConnecting}
	go s.run(ctx, entry, config, s.handler)
}

// Remove stops and forgets the connection identified by id entirely (unlike
// Set with Enabled: false, which keeps a disabled entry around).
func (s *ClientSupervisor) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[id]; ok && existing.cancel != nil {
		existing.cancel()
	}
	delete(s.entries, id)
}

// List returns the current status of every configured connection, sorted
// by id for a stable order across calls.
func (s *ClientSupervisor) List() []protocol.UpstreamStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	statuses := make([]protocol.UpstreamStatus, 0, len(s.entries))
	for _, entry := range s.entries {
		statuses = append(statuses, entry.status)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Config.ID < statuses[j].Config.ID })
	return statuses
}

// Get returns the current status of the connection identified by id.
func (s *ClientSupervisor) Get(id string) (protocol.UpstreamStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[id]
	if !ok {
		return protocol.UpstreamStatus{}, false
	}
	return entry.status, true
}

// update applies mutate to entry's status, but only if ctx (the calling run
// loop's context) has not been superseded by a newer Set/Remove call for
// the same id in the meantime — see Set's cancel-then-replace sequencing
// under the same mutex, which is what makes this check race-free.
func (s *ClientSupervisor) update(ctx context.Context, entry *supervisorEntry, mutate func(*protocol.UpstreamStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	mutate(&entry.status)
}

func (s *ClientSupervisor) run(ctx context.Context, entry *supervisorEntry, config protocol.UpstreamConfig, handler http.Handler) {
	backoff := supervisorInitialBackoff
	var failingSince time.Time

	for ctx.Err() == nil {
		s.update(ctx, entry, func(status *protocol.UpstreamStatus) {
			status.State = protocol.UpstreamStateConnecting
			status.LastError = ""
		})

		var connectedSession *yamux.Session
		connected, err := dialOnce(ctx, ClientOptions{
			UpstreamURL: config.UpstreamURL,
			NodeID:      config.NodeID,
			NodeName:    config.NodeName,
			NodeToken:   config.NodeToken,
			Handler:     handler,
			Logger:      s.logger,
			// dialOnce blocks for the connection's entire lifetime and only
			// returns once it has already ended, so "connected" state must
			// be set from this callback (fired the moment the handshake
			// succeeds), not from dialOnce's return value below.
			OnConnected: func(session *yamux.Session) {
				connectedSession = session
				now := s.now()
				s.update(ctx, entry, func(status *protocol.UpstreamStatus) {
					entry.session = session
					status.State = protocol.UpstreamStateConnected
					status.LastConnectedAt = &now
					status.LastError = ""
				})
			},
		}, s.logger)
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if entry.session == connectedSession {
			entry.session = nil
		}
		s.mu.Unlock()

		if connected {
			backoff = supervisorInitialBackoff
			failingSince = time.Time{}
			if err != nil {
				s.logger.Warn("relay: upstream connection ended", "upstream", config.UpstreamURL, "error", err)
			}
			// The session that just ended is no longer "connected"; the next
			// line's wait computation may relabel this "waiting" instead.
			s.update(ctx, entry, func(status *protocol.UpstreamStatus) {
				status.State = protocol.UpstreamStateConnecting
			})
		} else {
			if failingSince.IsZero() {
				failingSince = s.now()
			}
			message := ""
			if err != nil {
				message = err.Error()
			}
			s.logger.Info("relay: upstream connection attempt failed", "upstream", config.UpstreamURL, "error", err)
			s.update(ctx, entry, func(status *protocol.UpstreamStatus) {
				status.LastError = message
			})
		}

		quiet := !failingSince.IsZero() && s.now().Sub(failingSince) > supervisorQuietAfter
		wait := backoff
		if quiet {
			wait = supervisorQuietInterval
		}
		nextAttempt := s.now().Add(wait)
		s.update(ctx, entry, func(status *protocol.UpstreamStatus) {
			status.NextAttemptAt = &nextAttempt
			if quiet {
				status.State = protocol.UpstreamStateWaiting
			}
		})

		select {
		case <-ctx.Done():
			return
		case <-s.after(wait):
		}
		if !connected {
			backoff *= 2
			if backoff > supervisorFastMaxBackoff {
				backoff = supervisorFastMaxBackoff
			}
		}
	}
}

// Close stops every configured connection and forgets them. Used at process
// shutdown, before the loopback HTTP server itself shuts down.
func (s *ClientSupervisor) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.entries {
		if entry.cancel != nil {
			entry.cancel()
		}
	}
	s.entries = make(map[string]*supervisorEntry)
}

// UpstreamProxy returns a proxy to the upper Agent Manager identified by id,
// over that connection's live bidirectional yamux session. The same
// connection already carries requests from the upper node to this lower
// node; yamux permits streams in both directions, so no additional network
// listener is required.
func (s *ClientSupervisor) UpstreamProxy(id string) (http.Handler, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[id]
	if !ok || entry.session == nil {
		return nil, false
	}
	return NewReverseProxy(entry.session), true
}
