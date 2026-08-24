package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubcontroller"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

type fakeGitHubMonitorController struct {
	resolution protocol.GitHubRepositoryResolution
	monitors   []protocol.GitHubRepositoryMonitor
	monitor    protocol.GitHubRepositoryMonitor
	syncResult protocol.GitHubSyncResult
	rules      []protocol.GitHubTriggerRule
	rule       protocol.GitHubTriggerRule
	events     []protocol.GitHubMonitorEvent
	replay     protocol.GitHubMonitorEvent
	items      protocol.GitHubItemListResponse
	item       protocol.GitHubItem
	err        error

	lastWorkspace       string
	lastCreateMonitor   protocol.CreateGitHubMonitorRequest
	lastUpdateMonitor   protocol.UpdateGitHubMonitorRequest
	lastRuleRequest     protocol.GitHubTriggerRuleRequest
	lastRuleID          string
	lastReplayEventID   string
	lastSkipEventID     string
	lastListEventsLimit int
	lastItemQuery       githubcontroller.ItemQuery
	lastItemNumber      int
}

func (f *fakeGitHubMonitorController) ResolveRepository(ctx context.Context, workspace string) (protocol.GitHubRepositoryResolution, error) {
	f.lastWorkspace = workspace
	return f.resolution, f.err
}
func (f *fakeGitHubMonitorController) ListMonitors(ctx context.Context) ([]protocol.GitHubRepositoryMonitor, error) {
	return f.monitors, f.err
}
func (f *fakeGitHubMonitorController) CreateMonitor(ctx context.Context, request protocol.CreateGitHubMonitorRequest) (protocol.GitHubRepositoryMonitor, error) {
	f.lastCreateMonitor = request
	return f.monitor, f.err
}
func (f *fakeGitHubMonitorController) GetMonitor(ctx context.Context, workspace string) (protocol.GitHubRepositoryMonitor, error) {
	f.lastWorkspace = workspace
	return f.monitor, f.err
}
func (f *fakeGitHubMonitorController) UpdateMonitor(ctx context.Context, request protocol.UpdateGitHubMonitorRequest) (protocol.GitHubRepositoryMonitor, error) {
	f.lastUpdateMonitor = request
	return f.monitor, f.err
}
func (f *fakeGitHubMonitorController) DeleteMonitor(ctx context.Context, workspace string) error {
	f.lastWorkspace = workspace
	return f.err
}
func (f *fakeGitHubMonitorController) SyncNow(ctx context.Context, workspace string) (protocol.GitHubSyncResult, error) {
	f.lastWorkspace = workspace
	return f.syncResult, f.err
}
func (f *fakeGitHubMonitorController) ListRules(ctx context.Context, workspace string) ([]protocol.GitHubTriggerRule, error) {
	f.lastWorkspace = workspace
	return f.rules, f.err
}
func (f *fakeGitHubMonitorController) CreateRule(ctx context.Context, request protocol.GitHubTriggerRuleRequest) (protocol.GitHubTriggerRule, error) {
	f.lastRuleRequest = request
	return f.rule, f.err
}
func (f *fakeGitHubMonitorController) GetRule(ctx context.Context, id string) (protocol.GitHubTriggerRule, error) {
	f.lastRuleID = id
	return f.rule, f.err
}
func (f *fakeGitHubMonitorController) UpdateRule(ctx context.Context, id string, request protocol.GitHubTriggerRuleRequest) (protocol.GitHubTriggerRule, error) {
	f.lastRuleID = id
	f.lastRuleRequest = request
	return f.rule, f.err
}
func (f *fakeGitHubMonitorController) DeleteRule(ctx context.Context, id string) error {
	f.lastRuleID = id
	return f.err
}
func (f *fakeGitHubMonitorController) ListEvents(ctx context.Context, workspace string, limit int) ([]protocol.GitHubMonitorEvent, error) {
	f.lastWorkspace = workspace
	f.lastListEventsLimit = limit
	return f.events, f.err
}
func (f *fakeGitHubMonitorController) ReplayEvent(ctx context.Context, eventID string) (protocol.GitHubMonitorEvent, error) {
	f.lastReplayEventID = eventID
	return f.replay, f.err
}
func (f *fakeGitHubMonitorController) SkipEvent(ctx context.Context, eventID string) (protocol.GitHubMonitorEvent, error) {
	f.lastSkipEventID = eventID
	return f.replay, f.err
}
func (f *fakeGitHubMonitorController) ListIssues(ctx context.Context, workspace string, query githubcontroller.ItemQuery) (protocol.GitHubItemListResponse, error) {
	f.lastWorkspace = workspace
	f.lastItemQuery = query
	return f.items, f.err
}
func (f *fakeGitHubMonitorController) GetIssue(ctx context.Context, workspace string, number int) (protocol.GitHubItem, error) {
	f.lastWorkspace = workspace
	f.lastItemNumber = number
	return f.item, f.err
}
func (f *fakeGitHubMonitorController) ListPullRequests(ctx context.Context, workspace string, query githubcontroller.ItemQuery) (protocol.GitHubItemListResponse, error) {
	f.lastWorkspace = workspace
	f.lastItemQuery = query
	return f.items, f.err
}
func (f *fakeGitHubMonitorController) GetPullRequest(ctx context.Context, workspace string, number int) (protocol.GitHubItem, error) {
	f.lastWorkspace = workspace
	f.lastItemNumber = number
	return f.item, f.err
}

func TestGitHubResolveRepositoryAPI(t *testing.T) {
	controller := &fakeGitHubMonitorController{resolution: protocol.GitHubRepositoryResolution{
		Repository: "/repo", Candidates: []protocol.GitHubRemoteCandidate{{Host: "github.com", Owner: "octo-org", Name: "example", RemoteName: "origin"}},
	}}
	config := testConfig()
	config.GitHubMonitorController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/repository?workspace=%2Frepo"))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if controller.lastWorkspace != "/repo" {
		t.Fatalf("lastWorkspace = %q", controller.lastWorkspace)
	}
	var response protocol.GitHubRepositoryResolution
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Candidates) != 1 || response.Candidates[0].Owner != "octo-org" {
		t.Fatalf("response = %#v", response)
	}
}

func TestGitHubListMonitorsAPI(t *testing.T) {
	controller := &fakeGitHubMonitorController{monitors: []protocol.GitHubRepositoryMonitor{
		{Repository: "/repo-a", Host: "github.com", Owner: "octo-org", Name: "a", Enabled: true},
		{Repository: "/repo-b", Host: "github.com", Owner: "octo-org", Name: "b", Enabled: false},
	}}
	config := testConfig()
	config.GitHubMonitorController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/monitors"))
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.GitHubRepositoryMonitorListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Monitors) != 2 {
		t.Fatalf("response = %#v", response)
	}
}

func TestGitHubMonitorCRUDAndSyncAPI(t *testing.T) {
	controller := &fakeGitHubMonitorController{
		monitor:    protocol.GitHubRepositoryMonitor{Repository: "/repo", Host: "github.com", Owner: "octo-org", Name: "example"},
		syncResult: protocol.GitHubSyncResult{IssuesProcessed: 3, EventsMatched: 1},
	}
	config := testConfig()
	config.GitHubMonitorController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedJSONRequest("POST", "/api/v1/github/monitor", `{"workspace":"/repo","pollIntervalSeconds":300}`))
	if recorder.Code != 201 {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if controller.lastCreateMonitor.Workspace != "/repo" || controller.lastCreateMonitor.PollIntervalSeconds != 300 {
		t.Fatalf("lastCreateMonitor = %#v", controller.lastCreateMonitor)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/monitor?workspace=%2Frepo"))
	if recorder.Code != 200 {
		t.Fatalf("get status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedJSONRequest("PUT", "/api/v1/github/monitor", `{"workspace":"/repo","enabled":false,"pollIntervalSeconds":600,"coalesceQueueLimit":5}`))
	if recorder.Code != 200 {
		t.Fatalf("update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if controller.lastUpdateMonitor.PollIntervalSeconds != 600 || controller.lastUpdateMonitor.Enabled {
		t.Fatalf("lastUpdateMonitor = %#v", controller.lastUpdateMonitor)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("POST", "/api/v1/github/monitor/sync?workspace=%2Frepo"))
	if recorder.Code != 200 {
		t.Fatalf("sync status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var syncResponse protocol.GitHubSyncResult
	if err := json.NewDecoder(recorder.Body).Decode(&syncResponse); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if syncResponse.IssuesProcessed != 3 || syncResponse.EventsMatched != 1 {
		t.Fatalf("syncResponse = %#v", syncResponse)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("DELETE", "/api/v1/github/monitor?workspace=%2Frepo"))
	if recorder.Code != 204 {
		t.Fatalf("delete status = %d", recorder.Code)
	}
}

func TestGitHubMonitorErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", storage.ErrNotFound, 404},
		{"conflict", storage.ErrConflict, 409},
		{"ambiguous remote", githubcontroller.ErrAmbiguousRemote, 409},
		{"invalid request", githubcontroller.ErrInvalidRequest, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			controller := &fakeGitHubMonitorController{err: tc.err}
			config := testConfig()
			config.GitHubMonitorController = controller
			handler := New(config, nil, nil).Handler()

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/monitor?workspace=%2Frepo"))
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, tc.want, recorder.Body.String())
			}
		})
	}
}

func TestGitHubTriggerRuleAPI(t *testing.T) {
	controller := &fakeGitHubMonitorController{
		rules: []protocol.GitHubTriggerRule{{ID: "rule-1", Name: "Design"}},
		rule:  protocol.GitHubTriggerRule{ID: "rule-1", Name: "Design"},
	}
	config := testConfig()
	config.GitHubMonitorController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/rules?workspace=%2Frepo"))
	if recorder.Code != 200 {
		t.Fatalf("list status = %d", recorder.Code)
	}
	var listResponse protocol.GitHubTriggerRuleListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&listResponse); err != nil || len(listResponse.Rules) != 1 {
		t.Fatalf("listResponse = %#v, err = %v", listResponse, err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedJSONRequest("POST", "/api/v1/github/rules", `{
		"workspace":"/repo","name":"Design","enabled":true,"eventKinds":["issue"],
		"promptTemplate":"Design {{.Title}}","provider":"codex","concurrencyPolicy":"coalesce"
	}`))
	if recorder.Code != 201 {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/rules/rule-1"))
	if recorder.Code != 200 || controller.lastRuleID != "rule-1" {
		t.Fatalf("get status = %d, lastRuleID = %q", recorder.Code, controller.lastRuleID)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedJSONRequest("PUT", "/api/v1/github/rules/rule-1", `{
		"workspace":"/repo","name":"Renamed","enabled":false,"eventKinds":["issue"],
		"promptTemplate":"x","provider":"codex"
	}`))
	if recorder.Code != 200 {
		t.Fatalf("update status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("DELETE", "/api/v1/github/rules/rule-1"))
	if recorder.Code != 204 {
		t.Fatalf("delete status = %d", recorder.Code)
	}
}

func TestGitHubEventHistoryAndReplayAPI(t *testing.T) {
	controller := &fakeGitHubMonitorController{
		events: []protocol.GitHubMonitorEvent{{ID: "event-1", Status: protocol.GitHubMonitorEventSkipped}},
		replay: protocol.GitHubMonitorEvent{ID: "event-2", Status: protocol.GitHubMonitorEventQueued},
	}
	config := testConfig()
	config.GitHubMonitorController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/events?workspace=%2Frepo&limit=50"))
	if recorder.Code != 200 || controller.lastListEventsLimit != 50 {
		t.Fatalf("status = %d, limit = %d", recorder.Code, controller.lastListEventsLimit)
	}
	var listResponse protocol.GitHubMonitorEventListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&listResponse); err != nil || len(listResponse.Events) != 1 {
		t.Fatalf("listResponse = %#v, err = %v", listResponse, err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("POST", "/api/v1/github/events/event-1/replay"))
	if recorder.Code != 201 || controller.lastReplayEventID != "event-1" {
		t.Fatalf("status = %d, lastReplayEventID = %q", recorder.Code, controller.lastReplayEventID)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("POST", "/api/v1/github/events/event-1/skip"))
	if recorder.Code != 200 || controller.lastSkipEventID != "event-1" {
		t.Fatalf("skip status = %d, lastSkipEventID = %q", recorder.Code, controller.lastSkipEventID)
	}
}

func TestGitHubItemsAPI(t *testing.T) {
	controller := &fakeGitHubMonitorController{
		items: protocol.GitHubItemListResponse{Items: []protocol.GitHubItem{{Number: 1, Title: "t"}}},
		item:  protocol.GitHubItem{Number: 42, Title: "detail"},
	}
	config := testConfig()
	config.GitHubMonitorController = controller
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/issues?workspace=%2Frepo&state=open&label=bug&label=P1&assignee=alice"))
	if recorder.Code != 200 {
		t.Fatalf("list issues status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if controller.lastItemQuery.State != "open" || controller.lastItemQuery.Assignee != "alice" || len(controller.lastItemQuery.Labels) != 2 {
		t.Fatalf("lastItemQuery = %#v", controller.lastItemQuery)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/issues/42?workspace=%2Frepo"))
	if recorder.Code != 200 || controller.lastItemNumber != 42 {
		t.Fatalf("get issue status = %d, number = %d", recorder.Code, controller.lastItemNumber)
	}
	var item protocol.GitHubItem
	if err := json.NewDecoder(recorder.Body).Decode(&item); err != nil || item.Title != "detail" {
		t.Fatalf("item = %#v, err = %v", item, err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/issues/not-a-number?workspace=%2Frepo"))
	if recorder.Code != 400 {
		t.Fatalf("invalid number status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/pulls?workspace=%2Frepo"))
	if recorder.Code != 200 {
		t.Fatalf("list pulls status = %d", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/pulls/42?workspace=%2Frepo"))
	if recorder.Code != 200 || controller.lastItemNumber != 42 {
		t.Fatalf("get pull status = %d", recorder.Code)
	}
}

func TestGitHubMonitorRoutesDisabledWhenControllerNil(t *testing.T) {
	handler := New(testConfig(), nil, nil).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest("GET", "/api/v1/github/monitor?workspace=%2Frepo"))
	if recorder.Code != 404 {
		t.Fatalf("status = %d, want 404 when GitHubMonitorController is nil", recorder.Code)
	}
}
