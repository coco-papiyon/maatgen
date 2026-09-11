package relay

import (
	"context"
	"log/slog"
	"net/http"
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

// ClientSupervisor runs at most one lower-node outbound relay connection
// (Decision 1) at a time, and lets its target be changed at runtime — from
// a "Server" settings screen, not just --upstream-url at startup — without
// restarting the process. Configure stops whatever connection attempt loop
// is currently running (if any) before starting a new one, so there is
// never more than one live attempt for this process.
type ClientSupervisor struct {
	handler http.Handler
	logger  *slog.Logger
	// now and after are overridden by tests with a fake clock so the
	// multi-minute quiet-interval escalation can be exercised without a
	// real multi-minute wait.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu      sync.Mutex
	status  protocol.UpstreamStatus
	cancel  context.CancelFunc
	session *yamux.Session
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
		status:  protocol.UpstreamStatus{State: protocol.UpstreamStateDisabled},
	}
}

// SetHandler sets the handler served over the relay session once it becomes
// available. It exists because the handler (server.New(...).Handler())
// commonly depends on a server.Config whose UpstreamStatusReader/
// UpstreamConfigSetter fields close over this same ClientSupervisor —
// callers construct the supervisor with a nil handler, build that config,
// then call SetHandler once the handler exists, before the first Configure.
func (s *ClientSupervisor) SetHandler(handler http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = handler
}

// Configure replaces the current target. Disabling (Enabled: false) or
// leaving UpstreamURL/NodeID empty stops any running connection without
// starting a new one.
func (s *ClientSupervisor) Configure(config protocol.UpstreamConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.session = nil
	if !config.Enabled || config.UpstreamURL == "" || config.NodeID == "" {
		s.status = protocol.UpstreamStatus{Config: config, State: protocol.UpstreamStateDisabled}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.status = protocol.UpstreamStatus{Config: config, State: protocol.UpstreamStateConnecting}
	go s.run(ctx, config, s.handler)
}

func (s *ClientSupervisor) Status(context.Context) protocol.UpstreamStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// update applies mutate to the shared status, but only if ctx (the calling
// run loop's context) has not been superseded by a newer Configure call in
// the meantime — see Configure's cancel-then-replace sequencing under the
// same mutex, which is what makes this check race-free.
func (s *ClientSupervisor) update(ctx context.Context, mutate func(*protocol.UpstreamStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	mutate(&s.status)
}

func (s *ClientSupervisor) run(ctx context.Context, config protocol.UpstreamConfig, handler http.Handler) {
	backoff := supervisorInitialBackoff
	var failingSince time.Time

	for ctx.Err() == nil {
		s.update(ctx, func(status *protocol.UpstreamStatus) {
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
				s.update(ctx, func(status *protocol.UpstreamStatus) {
					s.session = session
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
		if s.session == connectedSession {
			s.session = nil
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
			s.update(ctx, func(status *protocol.UpstreamStatus) {
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
			s.update(ctx, func(status *protocol.UpstreamStatus) {
				status.LastError = message
			})
		}

		quiet := !failingSince.IsZero() && s.now().Sub(failingSince) > supervisorQuietAfter
		wait := backoff
		if quiet {
			wait = supervisorQuietInterval
		}
		nextAttempt := s.now().Add(wait)
		s.update(ctx, func(status *protocol.UpstreamStatus) {
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

// UpstreamProxy returns a proxy to the upper Agent Manager over the live
// bidirectional yamux session. The same connection already carries requests
// from the upper node to this lower node; yamux permits streams in both
// directions, so no additional network listener is required.
func (s *ClientSupervisor) UpstreamProxy() (http.Handler, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return nil, false
	}
	return NewReverseProxy(s.session), true
}
