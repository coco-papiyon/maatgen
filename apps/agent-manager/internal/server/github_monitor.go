package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubapi"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubcontroller"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/gitworktree"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

// GitHubMonitorController is the ADR-007 GitHub monitoring API surface. It
// is satisfied by *githubcontroller.Service.
type GitHubMonitorController interface {
	ResolveRepository(ctx context.Context, workspace string) (protocol.GitHubRepositoryResolution, error)
	ListMonitors(ctx context.Context) ([]protocol.GitHubRepositoryMonitor, error)
	CreateMonitor(ctx context.Context, request protocol.CreateGitHubMonitorRequest) (protocol.GitHubRepositoryMonitor, error)
	GetMonitor(ctx context.Context, workspace string) (protocol.GitHubRepositoryMonitor, error)
	UpdateMonitor(ctx context.Context, request protocol.UpdateGitHubMonitorRequest) (protocol.GitHubRepositoryMonitor, error)
	DeleteMonitor(ctx context.Context, workspace string) error
	SyncNow(ctx context.Context, workspace string) (protocol.GitHubSyncResult, error)

	ListRules(ctx context.Context, workspace string) ([]protocol.GitHubTriggerRule, error)
	CreateRule(ctx context.Context, request protocol.GitHubTriggerRuleRequest) (protocol.GitHubTriggerRule, error)
	GetRule(ctx context.Context, id string) (protocol.GitHubTriggerRule, error)
	UpdateRule(ctx context.Context, id string, request protocol.GitHubTriggerRuleRequest) (protocol.GitHubTriggerRule, error)
	DeleteRule(ctx context.Context, id string) error

	ListEvents(ctx context.Context, workspace string, limit int) ([]protocol.GitHubMonitorEvent, error)
	SkipEvent(ctx context.Context, eventID string) (protocol.GitHubMonitorEvent, error)
	ReplayEvent(ctx context.Context, eventID string) (protocol.GitHubMonitorEvent, error)

	ListIssues(ctx context.Context, workspace string, query githubcontroller.ItemQuery) (protocol.GitHubItemListResponse, error)
	GetIssue(ctx context.Context, workspace string, number int) (protocol.GitHubItem, error)
	ListPullRequests(ctx context.Context, workspace string, query githubcontroller.ItemQuery) (protocol.GitHubItemListResponse, error)
	GetPullRequest(ctx context.Context, workspace string, number int) (protocol.GitHubItem, error)
}

func registerGitHubMonitorRoutes(mux *http.ServeMux, controller GitHubMonitorController) {
	if controller == nil {
		return
	}

	mux.Handle("GET /api/v1/github/repository", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolution, err := controller.ResolveRepository(r.Context(), r.URL.Query().Get("workspace"))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resolution)
	}))

	mux.Handle("GET /api/v1/github/monitors", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		monitors, err := controller.ListMonitors(r.Context())
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		if monitors == nil {
			monitors = []protocol.GitHubRepositoryMonitor{}
		}
		writeJSON(w, http.StatusOK, protocol.GitHubRepositoryMonitorListResponse{Monitors: monitors})
	}))
	mux.Handle("GET /api/v1/github/monitor", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		monitor, err := controller.GetMonitor(r.Context(), r.URL.Query().Get("workspace"))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, monitor)
	}))
	mux.Handle("POST /api/v1/github/monitor", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request protocol.CreateGitHubMonitorRequest
		if err := readJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
			return
		}
		monitor, err := controller.CreateMonitor(r.Context(), request)
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, monitor)
	}))
	mux.Handle("PUT /api/v1/github/monitor", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request protocol.UpdateGitHubMonitorRequest
		if err := readJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
			return
		}
		monitor, err := controller.UpdateMonitor(r.Context(), request)
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, monitor)
	}))
	mux.Handle("DELETE /api/v1/github/monitor", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := controller.DeleteMonitor(r.Context(), r.URL.Query().Get("workspace")); err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.Handle("POST /api/v1/github/monitor/sync", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := controller.SyncNow(r.Context(), r.URL.Query().Get("workspace"))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))

	mux.Handle("GET /api/v1/github/rules", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rules, err := controller.ListRules(r.Context(), r.URL.Query().Get("workspace"))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		if rules == nil {
			rules = []protocol.GitHubTriggerRule{}
		}
		writeJSON(w, http.StatusOK, protocol.GitHubTriggerRuleListResponse{Rules: rules})
	}))
	mux.Handle("POST /api/v1/github/rules", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request protocol.GitHubTriggerRuleRequest
		if err := readJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
			return
		}
		rule, err := controller.CreateRule(r.Context(), request)
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, rule)
	}))
	mux.Handle("GET /api/v1/github/rules/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rule, err := controller.GetRule(r.Context(), r.PathValue("id"))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rule)
	}))
	mux.Handle("PUT /api/v1/github/rules/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request protocol.GitHubTriggerRuleRequest
		if err := readJSON(w, r, &request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
			return
		}
		rule, err := controller.UpdateRule(r.Context(), r.PathValue("id"), request)
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rule)
	}))
	mux.Handle("DELETE /api/v1/github/rules/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := controller.DeleteRule(r.Context(), r.PathValue("id")); err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	mux.Handle("GET /api/v1/github/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, ok := parseBoundedInt(w, r, "limit", 100, 1, 500)
		if !ok {
			return
		}
		events, err := controller.ListEvents(r.Context(), r.URL.Query().Get("workspace"), limit)
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		if events == nil {
			events = []protocol.GitHubMonitorEvent{}
		}
		writeJSON(w, http.StatusOK, protocol.GitHubMonitorEventListResponse{Events: events})
	}))
	mux.Handle("POST /api/v1/github/events/{id}/skip", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		event, err := controller.SkipEvent(r.Context(), r.PathValue("id"))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, event)
	}))
	mux.Handle("POST /api/v1/github/events/{id}/replay", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		replay, err := controller.ReplayEvent(r.Context(), r.PathValue("id"))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, replay)
	}))

	mux.Handle("GET /api/v1/github/issues", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, err := controller.ListIssues(r.Context(), r.URL.Query().Get("workspace"), parseItemQuery(r))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeItemListResponse(w, response)
	}))
	mux.Handle("GET /api/v1/github/issues/{number}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		number, ok := parsePathInt(w, r.PathValue("number"))
		if !ok {
			return
		}
		item, err := controller.GetIssue(r.Context(), r.URL.Query().Get("workspace"), number)
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}))
	mux.Handle("GET /api/v1/github/pulls", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, err := controller.ListPullRequests(r.Context(), r.URL.Query().Get("workspace"), parseItemQuery(r))
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeItemListResponse(w, response)
	}))
	mux.Handle("GET /api/v1/github/pulls/{number}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		number, ok := parsePathInt(w, r.PathValue("number"))
		if !ok {
			return
		}
		item, err := controller.GetPullRequest(r.Context(), r.URL.Query().Get("workspace"), number)
		if err != nil {
			writeGitHubMonitorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
	}))
}

func parseItemQuery(r *http.Request) githubcontroller.ItemQuery {
	query := r.URL.Query()
	return githubcontroller.ItemQuery{
		State:    query.Get("state"),
		Assignee: query.Get("assignee"),
		Author:   query.Get("author"),
		Labels:   query["label"],
		Text:     query.Get("text"),
		Project:  query.Get("project"),
		Status:   query.Get("status"),
	}
}

func writeItemListResponse(w http.ResponseWriter, response protocol.GitHubItemListResponse) {
	if response.Items == nil {
		response.Items = []protocol.GitHubItem{}
	}
	writeJSON(w, http.StatusOK, response)
}

func parsePathInt(w http.ResponseWriter, value string) (int, bool) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "number must be a positive integer", nil)
		return 0, false
	}
	return number, true
}

func writeGitHubMonitorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "resource was not found", nil)
	case errors.Is(err, storage.ErrConflict):
		writeAPIError(w, http.StatusConflict, "conflict", err.Error(), nil)
	case errors.Is(err, checkpoint.ErrNotRepository):
		writeAPIError(w, http.StatusBadRequest, "not_a_repository", "workspace is not a Git repository", nil)
	case errors.Is(err, gitworktree.ErrNoGitHubRemote):
		writeAPIError(w, http.StatusUnprocessableEntity, "no_github_remote", "no GitHub remote was found for this repository", nil)
	case errors.Is(err, githubcontroller.ErrAmbiguousRemote):
		writeAPIError(w, http.StatusConflict, "ambiguous_remote", "multiple GitHub remotes were found; specify remoteName", nil)
	case errors.Is(err, githubcontroller.ErrInvalidRequest):
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
	case errors.Is(err, githubapi.ErrAuthenticationRequired):
		writeAPIError(w, http.StatusUnauthorized, "github_auth_required", "GitHubの認証またはProject参照権限が必要です。初回は `gh auth login --scopes \"read:project\"`、既存の認証を更新する場合は `gh auth refresh -s read:project` を実行してから再試行してください。", nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "github_monitor_operation_failed", "github monitor operation failed", nil)
	}
}
