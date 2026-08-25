package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	restoreservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/restore"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
	sessionservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

type Config struct {
	Version                 string
	SchemaVersion           int
	DefaultWorkspace        string
	AllowedOrigins          []string
	TicketTTL               time.Duration
	EventSubscriber         EventSubscriber
	SessionCreator          SessionCreator
	SessionCloser           SessionCloser
	SessionReopener         SessionReopener
	RunController           RunController
	ChangeReader            ChangeReader
	RestoreController       RestoreController
	Providers               []protocol.Provider
	ModelSetter             ModelSetter
	UsageReader             UsageReader
	ProviderUsageReader     ProviderUsageReader
	UsageSummaryReader      UsageSummaryReader
	SourceStatsReader       SourceStatsReader
	ApprovalController      ApprovalController
	WorkspaceReader         WorkspaceReader
	GitHubMonitorController GitHubMonitorController
	// StaticFS serves the built Web UI. Requests outside /api and /ws fall
	// back to index.html so client-side routes resolve on a fresh load.
	// Static serving is disabled when nil.
	StaticFS fs.FS
}

type ModelSetter func(ctx context.Context, provider protocol.AgentName, model string) error

type setModelRequest struct {
	Model string `json:"model"`
}

type HealthResponse struct {
	Status        string    `json:"status"`
	Version       string    `json:"version"`
	SchemaVersion int       `json:"schemaVersion"`
	Time          time.Time `json:"time"`
}

type RuntimeConfigResponse struct {
	DefaultWorkspace string `json:"defaultWorkspace"`
}

type SessionReader interface {
	ListSessions(ctx context.Context, limit int, before *protocol.SessionCursor, status protocol.SessionStatus) ([]protocol.AgentSession, error)
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
}

type SessionCreator interface {
	CreateSession(ctx context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error)
}

type SessionCloser interface {
	CloseSession(ctx context.Context, id string) (protocol.AgentSession, error)
}

type SessionReopener interface {
	ReopenSession(ctx context.Context, id string) (protocol.AgentSession, error)
}

type WorkspaceReader interface {
	SearchWorkspaceFiles(ctx context.Context, sessionID string, query string) ([]string, error)
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

type UsageReader interface {
	GetSessionUsage(ctx context.Context, sessionID string) (protocol.SessionUsage, error)
}

type ProviderUsageReader interface {
	GetProviderUsage(ctx context.Context, provider protocol.AgentName, directory string) (protocol.ProviderUsage, error)
}

type UsageSummaryReader interface {
	GetUsageSummary(ctx context.Context, granularity, provider, model string) (protocol.UsageSummary, error)
	ListUsageProviders(ctx context.Context) ([]string, error)
	ListUsageModels(ctx context.Context, provider string) ([]string, error)
}

type SourceStatsReader interface {
	GetSourceStats(ctx context.Context, sessionID string) (protocol.SourceStats, error)
}

type ApprovalController interface {
	List(ctx context.Context, sessionID string, pendingOnly bool) (protocol.ApprovalListResponse, error)
	Decide(ctx context.Context, sessionID, approvalID string, request protocol.ApprovalDecisionRequest) (protocol.CommandApproval, error)
}

type RestoreController interface {
	RestoreHunk(ctx context.Context, sessionID, checkpointID, hunkID string) (protocol.ChangeSet, error)
	RestoreFile(ctx context.Context, sessionID, checkpointID, fileID string) (protocol.ChangeSet, error)
	RestoreAll(ctx context.Context, sessionID, checkpointID string) (protocol.ChangeSet, error)
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

type workspaceFilesResponse struct {
	Files []string `json:"files"`
}

func New(config Config, sessions SessionReader, events EventReader) *Server {
	if config.TicketTTL <= 0 {
		config.TicketTTL = 30 * time.Second
	}
	tickets := newTicketStore(config.TicketTTL)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{
			Status:        "ok",
			Version:       config.Version,
			SchemaVersion: config.SchemaVersion,
			Time:          time.Now().UTC(),
		})
	})
	mux.Handle("GET /api/v1/runtime-config", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, RuntimeConfigResponse{DefaultWorkspace: config.DefaultWorkspace})
	}))
	mux.Handle("GET /api/v1/providers", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providers := config.Providers
		if providers == nil {
			providers = []protocol.Provider{}
		}
		writeJSON(w, http.StatusOK, protocol.ProviderListResponse{Providers: providers})
	}))
	if config.ModelSetter != nil {
		mux.Handle("PUT /api/v1/providers/{id}/model", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request setModelRequest
			if err := readJSON(w, r, &request); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
				return
			}
			if err := config.ModelSetter(r.Context(), protocol.AgentName(r.PathValue("id")), request.Model); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "model_persistence_failed", "model preference could not be saved", nil)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	if config.SessionCreator != nil {
		mux.Handle("POST /api/v1/sessions", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		}))
	}
	if config.SessionCloser != nil {
		mux.Handle("POST /api/v1/sessions/{id}/close", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			closed, err := config.SessionCloser.CloseSession(r.Context(), r.PathValue("id"))
			if err != nil {
				writeSessionCloseError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, closed)
		}))
	}
	if config.SessionReopener != nil {
		mux.Handle("POST /api/v1/sessions/{id}/reopen", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reopened, err := config.SessionReopener.ReopenSession(r.Context(), r.PathValue("id"))
			if err != nil {
				writeSessionCloseError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, reopened)
		}))
	}
	if config.RunController != nil {
		mux.Handle("POST /api/v1/sessions/{id}/messages", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		}))
		mux.Handle("POST /api/v1/runs/{id}/cancel", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := config.RunController.CancelRun(r.Context(), r.PathValue("id")); err != nil {
				writeRunError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	if config.ApprovalController != nil {
		mux.Handle("GET /api/v1/sessions/{id}/approvals", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pendingOnly := r.URL.Query().Get("status") == "pending"
			response, err := config.ApprovalController.List(r.Context(), r.PathValue("id"), pendingOnly)
			if err != nil {
				writeStorageError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, response)
		}))
		mux.Handle("POST /api/v1/sessions/{id}/approvals/{approvalId}/decision", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request protocol.ApprovalDecisionRequest
			if err := readJSON(w, r, &request); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
				return
			}
			approval, err := config.ApprovalController.Decide(r.Context(), r.PathValue("id"), r.PathValue("approvalId"), request)
			if err != nil {
				if errors.Is(err, storage.ErrConflict) {
					writeAPIError(w, http.StatusConflict, "approval_conflict", err.Error(), nil)
					return
				}
				if errors.Is(err, storage.ErrNotFound) {
					writeStorageError(w, err)
					return
				}
				writeAPIError(w, http.StatusBadRequest, "invalid_approval", err.Error(), nil)
				return
			}
			writeJSON(w, http.StatusOK, approval)
		}))
	}

	if sessions != nil {
		mux.Handle("GET /api/v1/sessions", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limit, ok := parseBoundedInt(w, r, "limit", 100, 1, 500)
			if !ok {
				return
			}
			cursor, err := decodeSessionCursor(r.URL.Query().Get("cursor"))
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_cursor", "cursor is invalid", nil)
				return
			}
			status, ok := parseSessionStatusFilter(w, r)
			if !ok {
				return
			}
			items, err := sessions.ListSessions(r.Context(), limit+1, cursor, status)
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
		}))

		mux.Handle("GET /api/v1/sessions/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := sessions.GetSession(r.Context(), r.PathValue("id"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, session)
		}))
	}

	if sessions != nil && events != nil {
		mux.Handle("GET /api/v1/sessions/{id}/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		}))

		mux.Handle("POST /api/v1/ws-tickets", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			ticket, expiresAt, err := tickets.issue()
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "an internal error occurred", nil)
				return
			}
			writeJSON(w, http.StatusCreated, wsTicketResponse{Ticket: ticket, ExpiresAt: expiresAt})
		}))

		mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
			serveEventWebSocket(w, r, config, tickets, sessions, events, config.EventSubscriber)
		})
	}
	if sessions != nil && config.WorkspaceReader != nil {
		mux.Handle("GET /api/v1/sessions/{id}/workspace-files", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := sessions.GetSession(r.Context(), r.PathValue("id")); err != nil {
				writeStorageError(w, err)
				return
			}
			query := r.URL.Query().Get("query")
			files, err := config.WorkspaceReader.SearchWorkspaceFiles(r.Context(), r.PathValue("id"), query)
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "search_failed", "failed to search workspace files", nil)
				return
			}
			if files == nil {
				files = []string{}
			}
			writeJSON(w, http.StatusOK, workspaceFilesResponse{Files: files})
		}))
	}
	if sessions != nil && config.UsageReader != nil {
		mux.Handle("GET /api/v1/sessions/{id}/usage", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := sessions.GetSession(r.Context(), r.PathValue("id")); err != nil {
				writeStorageError(w, err)
				return
			}
			usage, err := config.UsageReader.GetSessionUsage(r.Context(), r.PathValue("id"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			if usage.Runs == nil {
				usage.Runs = []protocol.RunUsageEntry{}
			}
			writeJSON(w, http.StatusOK, usage)
		}))
	}
	if sessions != nil && config.ProviderUsageReader != nil {
		mux.Handle("GET /api/v1/sessions/{id}/provider-usage", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := sessions.GetSession(r.Context(), r.PathValue("id"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			usage, err := config.ProviderUsageReader.GetProviderUsage(r.Context(), session.Agent, session.Workspace)
			if err != nil {
				slog.Warn("failed to fetch provider usage", "provider", session.Agent, "session", session.ID, "error", err)
				writeAPIError(w, http.StatusServiceUnavailable, "provider_usage_unavailable", "provider usage is unavailable", nil)
				return
			}
			writeJSON(w, http.StatusOK, usage)
		}))
	}
	if config.UsageSummaryReader != nil {
		mux.Handle("GET /api/v1/usage/summary", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			granularity := r.URL.Query().Get("granularity")
			if granularity == "" {
				granularity = "day"
			}
			if granularity != "day" && granularity != "week" && granularity != "month" {
				writeAPIError(w, http.StatusBadRequest, "invalid_request", "granularity must be day, week, or month", nil)
				return
			}
			summary, err := config.UsageSummaryReader.GetUsageSummary(r.Context(), granularity, r.URL.Query().Get("provider"), r.URL.Query().Get("model"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			if summary.Periods == nil {
				summary.Periods = []protocol.UsagePeriod{}
			}
			writeJSON(w, http.StatusOK, summary)
		}))
		mux.Handle("GET /api/v1/usage/providers", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			providers, err := config.UsageSummaryReader.ListUsageProviders(r.Context())
			if err != nil {
				writeStorageError(w, err)
				return
			}
			if providers == nil {
				providers = []string{}
			}
			writeJSON(w, http.StatusOK, protocol.UsageProviderListResponse{Providers: providers})
		}))
		mux.Handle("GET /api/v1/usage/models", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			models, err := config.UsageSummaryReader.ListUsageModels(r.Context(), r.URL.Query().Get("provider"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			if models == nil {
				models = []string{}
			}
			writeJSON(w, http.StatusOK, protocol.UsageModelListResponse{Models: models})
		}))
	}
	if sessions != nil && config.SourceStatsReader != nil {
		mux.Handle("GET /api/v1/sessions/{id}/source-stats", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := sessions.GetSession(r.Context(), r.PathValue("id")); err != nil {
				writeStorageError(w, err)
				return
			}
			stats, err := config.SourceStatsReader.GetSourceStats(r.Context(), r.PathValue("id"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			if stats.Languages == nil {
				stats.Languages = []protocol.SourceStatsLanguage{}
			}
			writeJSON(w, http.StatusOK, stats)
		}))
	}
	if config.ChangeReader != nil {
		mux.Handle("GET /api/v1/sessions/{id}/changes", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.ChangeReader.GetChangeSet(r.Context(), r.PathValue("id"))
			if err != nil {
				writeStorageError(w, err)
				return
			}
			if changeSet.Files == nil {
				changeSet.Files = []protocol.FileChange{}
			}
			writeJSON(w, http.StatusOK, changeSet)
		}))
	}
	if config.RestoreController != nil {
		mux.Handle("POST /api/v1/sessions/{id}/checkpoints/{checkpointId}/restore", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.RestoreController.RestoreAll(r.Context(), r.PathValue("id"), r.PathValue("checkpointId"))
			if err != nil {
				writeRestoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, changeSet)
		}))
		mux.Handle("POST /api/v1/sessions/{id}/checkpoints/{checkpointId}/files/{fileId}/restore", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.RestoreController.RestoreFile(r.Context(), r.PathValue("id"), r.PathValue("checkpointId"), r.PathValue("fileId"))
			if err != nil {
				writeRestoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, changeSet)
		}))
		mux.Handle("POST /api/v1/sessions/{id}/checkpoints/{checkpointId}/hunks/{hunkId}/restore", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			changeSet, err := config.RestoreController.RestoreHunk(r.Context(), r.PathValue("id"), r.PathValue("checkpointId"), r.PathValue("hunkId"))
			if err != nil {
				writeRestoreError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, changeSet)
		}))
	}

	registerGitHubMonitorRoutes(mux, config.GitHubMonitorController)

	static := staticHandler(config.StaticFS)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" || strings.HasPrefix(r.URL.Path, "/api/") || static == nil {
			writeAPIError(w, http.StatusNotFound, "not_found", "resource was not found", nil)
			return
		}
		static.ServeHTTP(w, r)
	})

	return &Server{handler: mux}
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

// staticHandler serves the built Web UI from fsys. Any GET/HEAD request for a
// path that does not resolve to a file falls back to index.html, so
// client-side routes (e.g. a deep link into a session) load correctly on a
// fresh request. Returns nil when fsys is nil so callers can detect that
// static serving is disabled.
func staticHandler(fsys fs.FS) http.Handler {
	if fsys == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		} else if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			name = "index.html"
		}
		http.ServeFileFS(w, r, fsys, name)
	})
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

// parseSessionStatusFilter reads the "status" query parameter used to filter
// GET /api/v1/sessions. Closed sessions are hidden from the list by default
// (they remain fully counted in usage summaries, which query runs directly)
// but callers can pass status=all or status=closed to see them.
func parseSessionStatusFilter(w http.ResponseWriter, r *http.Request) (protocol.SessionStatus, bool) {
	switch r.URL.Query().Get("status") {
	case "", "active":
		return protocol.SessionActive, true
	case "closed":
		return protocol.SessionClosed, true
	case "all":
		return "", true
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "status must be one of: active, closed, all", nil)
		return "", false
	}
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
	case errors.Is(err, checkpoint.ErrNotRepository):
		writeAPIError(w, http.StatusUnprocessableEntity, "not_git_repository", "workspace is not a Git repository", nil)
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
		writeAPIError(w, http.StatusServiceUnavailable, "checkpoint_cleanup_failed", "checkpoint cleanup failed; retry the close request", nil)
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
	case errors.Is(err, runservice.ErrRepositoryBusy):
		writeAPIError(w, http.StatusConflict, "repository_busy", "repository already has an active run", nil)
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

func writeRestoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "checkpoint change was not found", nil)
	case errors.Is(err, restoreservice.ErrConflict):
		writeAPIError(w, http.StatusConflict, "checkpoint_conflict", "current content changed after the checkpoint snapshot", nil)
	case errors.Is(err, restoreservice.ErrSessionClosed):
		writeAPIError(w, http.StatusConflict, "session_closed", "session is closed", nil)
	case errors.Is(err, restoreservice.ErrNotRestorable):
		writeAPIError(w, http.StatusUnprocessableEntity, "not_restorable", "change cannot be restored", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "restore_failed", "restore operation failed", nil)
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
