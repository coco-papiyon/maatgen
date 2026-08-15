package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/gitworktree"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	reviewservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/review"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
	sessionservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

type Config struct {
	Version          string
	SchemaVersion    int
	AuthToken        string
	AllowedOrigins   []string
	TicketTTL        time.Duration
	EventSubscriber  EventSubscriber
	SessionCreator   SessionCreator
	SessionCloser    SessionCloser
	RunController    RunController
	ChangeReader     ChangeReader
	ReviewController ReviewController
	Providers        []protocol.Provider
}

type HealthResponse struct {
	Status        string    `json:"status"`
	Version       string    `json:"version"`
	SchemaVersion int       `json:"schemaVersion"`
	Time          time.Time `json:"time"`
}

type SessionReader interface {
	ListSessions(ctx context.Context, limit int, before *protocol.SessionCursor) ([]protocol.AgentSession, error)
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
}

type SessionCreator interface {
	CreateSession(ctx context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error)
}

type SessionCloser interface {
	CloseSession(ctx context.Context, id string) (protocol.AgentSession, error)
}

type RunController interface {
	StartRun(ctx context.Context, sessionID string, request protocol.SendMessageRequest) (protocol.AgentRun, error)
	CancelRun(ctx context.Context, runID string) error
}

type EventReader interface {
	ListEventsAfter(ctx context.Context, sessionID string, afterSequence int64, limit int) ([]protocol.SessionEvent, error)
}

type ChangeReader interface {
	GetChangeSet(ctx context.Context, sessionID string) (protocol.ChangeSet, error)
}

type ReviewController interface {
	Accept(ctx context.Context, sessionID, changeID string) (protocol.ChangeSet, error)
	Reject(ctx context.Context, sessionID, changeID string) (protocol.ChangeSet, error)
	AcceptAll(ctx context.Context, sessionID string) (protocol.ChangeSet, error)
	RejectAll(ctx context.Context, sessionID string) (protocol.ChangeSet, error)
}

type EventSubscriber interface {
	Subscribe(sessionID string) (<-chan struct{}, func())
}

type Server struct {
	handler http.Handler
}

type sessionListResponse struct {
	Sessions   []protocol.AgentSession `json:"sessions"`
	NextCursor string                  `json:"nextCursor,omitempty"`
}

type eventListResponse struct {
	Events []protocol.SessionEvent `json:"events"`
}

func New(config Config, sessions SessionReader, events EventReader) *Server {
	if config.TicketTTL <= 0 {
		config.TicketTTL = 30 * time.Second
	}
	tickets := newTicketStore(config.TicketTTL)
	authenticate := bearerAuth(config.AuthToken)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{
			Status:        "ok",
			Version:       config.Version,
			SchemaVersion: config.SchemaVersion,
			Time:          time.Now().UTC(),
		})
	})
	mux.Handle("GET /api/v1/providers", authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providers := config.Providers
		if providers == nil {
			providers = []protocol.Provider{}
		}
		writeJSON(w, http.StatusOK, protocol.ProviderListResponse{Providers: providers})
	})))
	if config.SessionCreator != nil {
		mux.Handle("POST /api/v1/sessions", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request protocol.CreateSessionRequest
			if err := readJSON(w, r, &request); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
				return
			}
			created, err := config.SessionCreator.CreateSession(r.Context(), request)
			if err != nil {
				writeSessionCreateError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, created)
		})))
	}
	if config.SessionCloser != nil {
		mux.Handle("POST /api/v1/sessions/{id}/close", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			closed, err := config.SessionCloser.CloseSession(r.Context(), r.PathValue("id"))
			if err != nil {
				writeSessionCloseError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, closed)
		})))
	}
	if config.RunController != nil {
		mux.Handle("POST /api/v1/sessions/{id}/messages", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request protocol.SendMessageRequest
			if err := readJSON(w, r, &request); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
				return
			}
			run, err := config.RunController.StartRun(r.Context(), r.PathValue("id"), request)
			if err != nil {
				writeRunError(w, err)
				return
			}
			writeJSON(w, http.StatusAccepted, run)
		})))
		mux.Handle("POST /api/v1/runs/{id}/cancel", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := config.RunController.CancelRun(r.Context(), r.PathValue("id")); err != nil {
				writeRunError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})))
	}

	if sessions != nil {
		mux.Handle("GET /api/v1/sessions", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, ok := parseBoundedInt(w, r, "limit", 100, 1, 500)
			if !ok {
				return
			}
			cursor, err := decodeSessionCursor(r.URL.Query().Get("cursor"))
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid", nil)
				return
			}
			items, err := sessions.ListSessions(r.Context(), limit+1, cursor)
			if err != nil {
				writeStorageError(w, err)
				return
			}
			nextCursor := ""
			if len(items) > limit {
				items = items[:limit]
				nextCursor, err = encodeSessionCursor(items[len(items)-1])
				if err != nil {
					writeAPIError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
					return
				}
			}
			if items == nil {
				items = []protocol.AgentSession{}
			}
			writeJSON(w, http.StatusOK, sessionListResponse{Sessions: items, NextCursor: nextCursor})
		})))

		mux.Handle("GET /api/v1/sessions/{id}", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := sessions.GetSession(r.Context(), r.PathValue("id"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, session)
		})))
	}

	if sessions != nil && events != nil {
		mux.Handle("GET /api/v1/sessions/{id}/events", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := sessions.GetSession(r.Context(), r.PathValue("id")); err != nil {
				writeStorageError(w, err)
				return
			}
			after, ok := parseBoundedInt64(w, r, "afterSequence", 0, 0)
			if !ok {
				return
			}
			limit, ok := parseBoundedInt(w, r, "limit", 200, 1, 1000)
			if !ok {
				return
			}
			items, err := events.ListEventsAfter(r.Context(), r.PathValue("id"), after, limit)
			if err != nil {
				writeStorageError(w, err)
				return
			}
			if items == nil {
				items = []protocol.SessionEvent{}
			}
			writeJSON(w, http.StatusOK, eventListResponse{Events: items})
		})))

		mux.Handle("POST /api/v1/ws-tickets", authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			ticket, expiresAt, err := tickets.issue()
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
				return
			}
			writeJSON(w, http.StatusCreated, wsTicketResponse{Ticket: ticket, ExpiresAt: expiresAt})
		})))

		mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
			serveEventWebSocket(w, r, config, tickets, sessions, events, config.EventSubscriber)
		})
	}
	if config.ChangeReader != nil {
		mux.Handle("GET /api/v1/sessions/{id}/changes", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.ChangeReader.GetChangeSet(r.Context(), r.PathValue("id"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			if changeSet.Files == nil {
				changeSet.Files = []protocol.FileChange{}
			}
			writeJSON(w, http.StatusOK, changeSet)
		})))
	}
	if config.ReviewController != nil {
		mux.Handle("POST /api/v1/sessions/{id}/changes/accept-all", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.ReviewController.AcceptAll(r.Context(), r.PathValue("id"))
			if err != nil {
				writeReviewError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, changeSet)
		})))
		mux.Handle("POST /api/v1/sessions/{id}/changes/reject-all", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.ReviewController.RejectAll(r.Context(), r.PathValue("id"))
			if err != nil {
				writeReviewError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, changeSet)
		})))
		mux.Handle("POST /api/v1/sessions/{id}/changes/{changeId}/accept", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.ReviewController.Accept(r.Context(), r.PathValue("id"), r.PathValue("changeId"))
			if err != nil {
				writeReviewError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, changeSet)
		})))
		mux.Handle("POST /api/v1/sessions/{id}/changes/{changeId}/reject", authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.ReviewController.Reject(r.Context(), r.PathValue("id"), r.PathValue("changeId"))
			if err != nil {
				writeReviewError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, changeSet)
		})))
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found", "resource was not found", nil)
	})

	return &Server{handler: mux}
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func parseBoundedInt(w http.ResponseWriter, r *http.Request, name string, fallback, minimum, maximum int) (int, bool) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return fallback, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", name+" is out of range", nil)
		return 0, false
	}
	return parsed, true
}

func parseBoundedInt64(w http.ResponseWriter, r *http.Request, name string, fallback, minimum int64) (int64, bool) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return fallback, true
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < minimum {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", name+" is out of range", nil)
		return 0, false
	}
	return parsed, true
}

func writeStorageError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "not_found", "resource was not found", nil)
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
}

func writeSessionCreateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sessionservice.ErrInvalidRequest), errors.Is(err, sessionservice.ErrUnsupportedAgent):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
	case errors.Is(err, gitworktree.ErrNotRepository):
		writeAPIError(w, http.StatusUnprocessableEntity, "not_git_repository", "workspace is not a Git repository", nil)
	case errors.Is(err, gitworktree.ErrDirty):
		writeAPIError(w, http.StatusConflict, "workspace_not_clean", "workspace has uncommitted changes", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "session_creation_failed", "session could not be created", nil)
	}
}

func writeSessionCloseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "resource was not found", nil)
	case errors.Is(err, sessionservice.ErrInvalidRequest):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
	case errors.Is(err, sessionservice.ErrRunActive):
		writeAPIError(w, http.StatusConflict, "run_already_active", "session has an active run", nil)
	case errors.Is(err, sessionservice.ErrCleanupFailed):
		writeAPIError(w, http.StatusServiceUnavailable, "worktree_cleanup_failed", "worktree cleanup failed; retry the close request", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "session_close_failed", "session could not be closed", nil)
	}
}

func writeRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "resource was not found", nil)
	case errors.Is(err, runservice.ErrInvalidRequest):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
	case errors.Is(err, runservice.ErrRunActive):
		writeAPIError(w, http.StatusConflict, "run_already_active", "session already has an active run", nil)
	case errors.Is(err, runservice.ErrSessionClosed):
		writeAPIError(w, http.StatusConflict, "session_closed", "session is closed", nil)
	case errors.Is(err, runservice.ErrRunNotActive):
		writeAPIError(w, http.StatusConflict, "run_not_active", "run is not active", nil)
	case errors.Is(err, runservice.ErrServiceClosed):
		writeAPIError(w, http.StatusServiceUnavailable, "service_unavailable", "run service is shutting down", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "run_operation_failed", "run operation failed", nil)
	}
}

func writeReviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "change was not found", nil)
	case errors.Is(err, reviewservice.ErrConflict):
		writeAPIError(w, http.StatusConflict, "working_tree_conflict", "working tree changed outside maatgen", nil)
	case errors.Is(err, reviewservice.ErrAlreadyReviewed):
		writeAPIError(w, http.StatusConflict, "already_reviewed", "change was already reviewed differently", nil)
	case errors.Is(err, reviewservice.ErrSessionClosed):
		writeAPIError(w, http.StatusConflict, "session_closed", "session is closed", nil)
	case errors.Is(err, reviewservice.ErrUnsupported):
		writeAPIError(w, http.StatusUnprocessableEntity, "unsupported_change", "change type cannot be applied", nil)
	case errors.Is(err, sessionservice.ErrCleanupFailed):
		writeAPIError(w, http.StatusServiceUnavailable, "worktree_cleanup_failed", "review completed but worktree cleanup failed; retry session close", nil)
	case errors.Is(err, sessionservice.ErrRunActive):
		writeAPIError(w, http.StatusConflict, "run_already_active", "session has an active run", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "review_failed", "review operation failed", nil)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, protocol.APIErrorResponse{
		Error: protocol.APIErrorBody{Code: code, Message: message, Details: details},
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
