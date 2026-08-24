package githubcontroller

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubapi"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubmonitor"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

// fakeValidator treats workspace as already-normalized, matching
// checkpoint.Manager.ValidateRepository's contract closely enough for
// these tests (which don't touch real Git commands).
type fakeValidator struct{ err error }

func (f fakeValidator) ValidateRepository(ctx context.Context, workspace string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return workspace, nil
}

type fakeStore struct {
	monitors map[string]protocol.GitHubRepositoryMonitor
	rules    map[string]protocol.GitHubTriggerRule
	events   map[string]protocol.GitHubMonitorEvent
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		monitors: map[string]protocol.GitHubRepositoryMonitor{},
		rules:    map[string]protocol.GitHubTriggerRule{},
		events:   map[string]protocol.GitHubMonitorEvent{},
	}
}

func (f *fakeStore) CreateRepositoryMonitor(ctx context.Context, monitor protocol.GitHubRepositoryMonitor) error {
	if _, exists := f.monitors[monitor.Repository]; exists {
		return storage.ErrConflict
	}
	f.monitors[monitor.Repository] = monitor
	return nil
}

func (f *fakeStore) GetRepositoryMonitor(ctx context.Context, repository string) (protocol.GitHubRepositoryMonitor, error) {
	monitor, ok := f.monitors[repository]
	if !ok {
		return protocol.GitHubRepositoryMonitor{}, storage.ErrNotFound
	}
	return monitor, nil
}

func (f *fakeStore) UpdateRepositoryMonitorSettings(ctx context.Context, monitor protocol.GitHubRepositoryMonitor, updatedAt time.Time) error {
	if _, ok := f.monitors[monitor.Repository]; !ok {
		return storage.ErrNotFound
	}
	f.monitors[monitor.Repository] = monitor
	return nil
}

func (f *fakeStore) DeleteRepositoryMonitor(ctx context.Context, repository string) error {
	if _, ok := f.monitors[repository]; !ok {
		return storage.ErrNotFound
	}
	delete(f.monitors, repository)
	return nil
}

func (f *fakeStore) CreateTriggerRule(ctx context.Context, rule protocol.GitHubTriggerRule) error {
	f.rules[rule.ID] = rule
	return nil
}

func (f *fakeStore) UpdateTriggerRule(ctx context.Context, rule protocol.GitHubTriggerRule) error {
	if _, ok := f.rules[rule.ID]; !ok {
		return storage.ErrNotFound
	}
	f.rules[rule.ID] = rule
	return nil
}

func (f *fakeStore) GetTriggerRule(ctx context.Context, id string) (protocol.GitHubTriggerRule, error) {
	rule, ok := f.rules[id]
	if !ok {
		return protocol.GitHubTriggerRule{}, storage.ErrNotFound
	}
	return rule, nil
}

func (f *fakeStore) ListTriggerRules(ctx context.Context, repository string) ([]protocol.GitHubTriggerRule, error) {
	var result []protocol.GitHubTriggerRule
	for _, rule := range f.rules {
		if rule.Repository == repository {
			result = append(result, rule)
		}
	}
	return result, nil
}

func (f *fakeStore) DeleteTriggerRule(ctx context.Context, id string) error {
	if _, ok := f.rules[id]; !ok {
		return storage.ErrNotFound
	}
	delete(f.rules, id)
	return nil
}

func (f *fakeStore) ListMonitorEvents(ctx context.Context, repository string, limit int) ([]protocol.GitHubMonitorEvent, error) {
	var result []protocol.GitHubMonitorEvent
	for _, event := range f.events {
		if event.Repository == repository {
			result = append(result, event)
		}
	}
	return result, nil
}

func (f *fakeStore) GetMonitorEvent(ctx context.Context, id string) (protocol.GitHubMonitorEvent, error) {
	event, ok := f.events[id]
	if !ok {
		return protocol.GitHubMonitorEvent{}, storage.ErrNotFound
	}
	return event, nil
}

func (f *fakeStore) SkipMonitorEvent(ctx context.Context, id, reason string, updatedAt time.Time) error {
	event, ok := f.events[id]
	if !ok {
		return storage.ErrNotFound
	}
	event.Status = protocol.GitHubMonitorEventSkipped
	event.SkipReason = &reason
	event.UpdatedAt = updatedAt
	f.events[id] = event
	return nil
}

func (f *fakeStore) CreateReplayEvent(ctx context.Context, originalEventID, newEventID string, createdAt time.Time) (protocol.GitHubMonitorEvent, error) {
	original, ok := f.events[originalEventID]
	if !ok {
		return protocol.GitHubMonitorEvent{}, storage.ErrNotFound
	}
	replay := original
	replay.ID = newEventID
	replay.DeliveryKey = nil
	replay.ReplayOfEventID = &originalEventID
	replay.Status = protocol.GitHubMonitorEventQueued
	replay.CreatedAt = createdAt
	replay.UpdatedAt = createdAt
	f.events[newEventID] = replay
	return replay, nil
}

func initGitRepoWithRemotes(t *testing.T, remotes map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	run := func(args ...string) {
		cmd := exec.Command(gitPath, append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	run("init")
	for name, url := range remotes {
		run("remote", "add", name, url)
	}
	return repo
}

func TestResolveRepositorySingleRemoteAndExistingMonitor(t *testing.T) {
	ctx := context.Background()
	repo := initGitRepoWithRemotes(t, map[string]string{"origin": "https://github.com/octo-org/example.git"})
	store := newFakeStore()
	gitPath, _ := exec.LookPath("git")
	service := New(store, fakeValidator{}, gitPath, nil, nil, nil)

	resolution, err := service.ResolveRepository(ctx, repo)
	if err != nil {
		t.Fatalf("ResolveRepository: %v", err)
	}
	if resolution.Selected == nil || resolution.Selected.Owner != "octo-org" || resolution.Selected.RemoteName != "origin" {
		t.Fatalf("resolution = %#v", resolution)
	}
	if resolution.Monitor != nil {
		t.Fatalf("expected no monitor registered yet")
	}

	monitor := protocol.GitHubRepositoryMonitor{Repository: repo, Host: "github.com", Owner: "octo-org", Name: "example", RemoteName: "origin"}
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	resolution, err = service.ResolveRepository(ctx, repo)
	if err != nil {
		t.Fatalf("ResolveRepository: %v", err)
	}
	if resolution.Monitor == nil || resolution.Monitor.Owner != "octo-org" {
		t.Fatalf("resolution.Monitor = %#v", resolution.Monitor)
	}
}

func TestCreateMonitorRequiresRemoteNameWhenAmbiguous(t *testing.T) {
	ctx := context.Background()
	repo := initGitRepoWithRemotes(t, map[string]string{
		"a": "https://github.com/octo-org/example.git",
		"b": "https://github.com/other-org/example.git",
	})
	gitPath, _ := exec.LookPath("git")
	service := New(newFakeStore(), fakeValidator{}, gitPath, nil, nil, nil)

	_, err := service.CreateMonitor(ctx, protocol.CreateGitHubMonitorRequest{Workspace: repo, PollIntervalSeconds: 300})
	if !errors.Is(err, ErrAmbiguousRemote) {
		t.Fatalf("err = %v, want ErrAmbiguousRemote", err)
	}

	remoteName := "b"
	monitor, err := service.CreateMonitor(ctx, protocol.CreateGitHubMonitorRequest{Workspace: repo, RemoteName: &remoteName, PollIntervalSeconds: 300})
	if err != nil {
		t.Fatalf("CreateMonitor with explicit remoteName: %v", err)
	}
	if monitor.Owner != "other-org" || monitor.CoalesceQueueLimit != defaultCoalesceQueueLimit {
		t.Fatalf("monitor = %#v", monitor)
	}
}

func TestCreateMonitorRejectsInvalidPollInterval(t *testing.T) {
	ctx := context.Background()
	repo := initGitRepoWithRemotes(t, map[string]string{"origin": "https://github.com/octo-org/example.git"})
	gitPath, _ := exec.LookPath("git")
	service := New(newFakeStore(), fakeValidator{}, gitPath, nil, nil, nil)

	if _, err := service.CreateMonitor(ctx, protocol.CreateGitHubMonitorRequest{Workspace: repo, PollIntervalSeconds: 0}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("err = %v, want ErrInvalidRequest", err)
	}
}

func TestUpdateMonitorCanReResolveRemote(t *testing.T) {
	ctx := context.Background()
	repo := initGitRepoWithRemotes(t, map[string]string{
		"origin":   "https://github.com/octo-org/example.git",
		"upstream": "https://github.com/other-org/example.git",
	})
	gitPath, _ := exec.LookPath("git")
	store := newFakeStore()
	service := New(store, fakeValidator{}, gitPath, nil, nil, nil)

	created, err := service.CreateMonitor(ctx, protocol.CreateGitHubMonitorRequest{Workspace: repo, PollIntervalSeconds: 300})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if created.Owner != "octo-org" {
		t.Fatalf("created.Owner = %q", created.Owner)
	}

	upstream := "upstream"
	updated, err := service.UpdateMonitor(ctx, protocol.UpdateGitHubMonitorRequest{
		Workspace: repo, Enabled: false, PollIntervalSeconds: 600, RemoteName: &upstream,
	})
	if err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	if updated.Owner != "other-org" || updated.RemoteName != "upstream" || updated.Enabled || updated.PollIntervalSeconds != 600 {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestSyncNowDelegatesToPoller(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	monitor := protocol.GitHubRepositoryMonitor{Repository: "/repo", Host: "github.com", Owner: "octo-org", Name: "example"}
	_ = store.CreateRepositoryMonitor(ctx, monitor)
	poller := &fakePoller{result: githubmonitor.SyncResult{IssuesProcessed: 2, EventsMatched: 1}}
	service := New(store, fakeValidator{}, "", nil, poller, nil)

	result, err := service.SyncNow(ctx, "/repo")
	if err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if result.IssuesProcessed != 2 || result.EventsMatched != 1 {
		t.Fatalf("result = %#v", result)
	}
	if poller.calledRepository != "/repo" {
		t.Fatalf("poller called with %q", poller.calledRepository)
	}
}

type fakePoller struct {
	result           githubmonitor.SyncResult
	err              error
	calledRepository string
}

func (f *fakePoller) SyncRepository(ctx context.Context, repository string) (githubmonitor.SyncResult, error) {
	f.calledRepository = repository
	if f.err != nil {
		return githubmonitor.SyncResult{}, f.err
	}
	return f.result, nil
}

func TestCreateRuleValidatesTemplateAndProvider(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	service := New(store, fakeValidator{}, "", nil, nil, nil)

	base := protocol.GitHubTriggerRuleRequest{
		Workspace: "/repo", Name: "rule", EventKinds: []protocol.GitHubItemKind{protocol.GitHubItemIssue},
		PromptTemplate: "Design {{.Title}}", Provider: protocol.AgentCodex,
	}

	missingName := base
	missingName.Name = ""
	if _, err := service.CreateRule(ctx, missingName); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing name err = %v", err)
	}

	badTemplate := base
	badTemplate.PromptTemplate = "{{.Title"
	if _, err := service.CreateRule(ctx, badTemplate); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("bad template err = %v", err)
	}

	badProvider := base
	badProvider.Provider = "not-a-provider"
	if _, err := service.CreateRule(ctx, badProvider); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("bad provider err = %v", err)
	}

	created, err := service.CreateRule(ctx, base)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if created.ConcurrencyPolicy != protocol.GitHubConcurrencyCoalesce {
		t.Fatalf("default concurrencyPolicy = %v, want coalesce", created.ConcurrencyPolicy)
	}
}

func TestReplayEventCreatesNewQueuedEventWithoutTouchingOriginal(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	deliveryKey := "original-key"
	original := protocol.GitHubMonitorEvent{
		ID: "event-1", Repository: "/repo", Status: protocol.GitHubMonitorEventSkipped, DeliveryKey: &deliveryKey,
	}
	store.events[original.ID] = original
	service := New(store, fakeValidator{}, "", nil, nil, nil)

	replay, err := service.ReplayEvent(ctx, "event-1")
	if err != nil {
		t.Fatalf("ReplayEvent: %v", err)
	}
	if replay.ID == "event-1" || replay.Status != protocol.GitHubMonitorEventQueued {
		t.Fatalf("replay = %#v", replay)
	}
	if replay.ReplayOfEventID == nil || *replay.ReplayOfEventID != "event-1" {
		t.Fatalf("replay.ReplayOfEventID = %#v", replay.ReplayOfEventID)
	}
	if replay.DeliveryKey != nil {
		t.Fatalf("replay.DeliveryKey = %#v, want nil", replay.DeliveryKey)
	}
	unchanged := store.events["event-1"]
	if unchanged.DeliveryKey == nil || *unchanged.DeliveryKey != deliveryKey {
		t.Fatalf("original event's delivery key changed: %#v", unchanged.DeliveryKey)
	}
}

func TestMatchesItemQuery(t *testing.T) {
	item := protocol.GitHubItem{
		Title: "Fix the widget", Body: "详情",
		Author:    protocol.GitHubUser{Login: "alice"},
		Assignees: []protocol.GitHubUser{{Login: "bob"}},
		Labels:    []protocol.GitHubLabel{{Name: "bug"}},
	}
	cases := []struct {
		name  string
		query ItemQuery
		want  bool
	}{
		{"no filters", ItemQuery{}, true},
		{"assignee match", ItemQuery{Assignee: "Bob"}, true},
		{"assignee mismatch", ItemQuery{Assignee: "carol"}, false},
		{"author match", ItemQuery{Author: "alice"}, true},
		{"label match", ItemQuery{Labels: []string{"bug"}}, true},
		{"label mismatch", ItemQuery{Labels: []string{"enhancement"}}, false},
		{"text match title", ItemQuery{Text: "widget"}, true},
		{"text mismatch", ItemQuery{Text: "nonexistent"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchesItemQuery(item, tc.query); got != tc.want {
				t.Fatalf("matchesItemQuery() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMatchesItemQueryProjectRequiresData(t *testing.T) {
	item := protocol.GitHubItem{Title: "t"}
	if matchesItemQuery(item, ItemQuery{Status: "Ready"}) {
		t.Fatalf("expected no match: item has no Project data at all")
	}
	item.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}}
	if !matchesItemQuery(item, ItemQuery{Status: "ready"}) {
		t.Fatalf("expected a case-insensitive Status match")
	}
	if matchesItemQuery(item, ItemQuery{Project: "Other"}) {
		t.Fatalf("expected no match for a different project title")
	}
}

func TestListIssuesFetchesProjectsOnlyWhenQueried(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	monitor := protocol.GitHubRepositoryMonitor{Repository: "/repo", Host: "github.com", Owner: "octo-org", Name: "example"}
	_ = store.CreateRepositoryMonitor(ctx, monitor)
	client := &fakeGitHubClient{issues: []protocol.GitHubItem{{Kind: protocol.GitHubItemIssue, Number: 1, Title: "t"}}}
	service := New(store, fakeValidator{}, "", nil, nil, func(string) (GitHubClient, error) { return client, nil })

	if _, err := service.ListIssues(ctx, "/repo", ItemQuery{}); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if client.projectFieldCalls != 0 {
		t.Fatalf("projectFieldCalls = %d, want 0 when the query does not filter on Project/Status", client.projectFieldCalls)
	}

	if _, err := service.ListIssues(ctx, "/repo", ItemQuery{Status: "Ready"}); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if client.projectFieldCalls != 1 {
		t.Fatalf("projectFieldCalls = %d, want 1", client.projectFieldCalls)
	}
}

type fakeGitHubClient struct {
	issues            []protocol.GitHubItem
	pulls             []protocol.GitHubItem
	projectFieldCalls int
}

func (f *fakeGitHubClient) ListIssues(ctx context.Context, owner, repo string, opts githubapi.ListOptions) ([]protocol.GitHubItem, error) {
	return f.issues, nil
}
func (f *fakeGitHubClient) GetIssue(ctx context.Context, owner, repo string, number int) (protocol.GitHubItem, error) {
	for _, item := range f.issues {
		if item.Number == number {
			return item, nil
		}
	}
	return protocol.GitHubItem{}, fmt.Errorf("not found")
}
func (f *fakeGitHubClient) ListPullRequests(ctx context.Context, owner, repo string, opts githubapi.ListOptions) ([]protocol.GitHubItem, error) {
	return f.pulls, nil
}
func (f *fakeGitHubClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (protocol.GitHubItem, error) {
	for _, item := range f.pulls {
		if item.Number == number {
			return item, nil
		}
	}
	return protocol.GitHubItem{}, fmt.Errorf("not found")
}
func (f *fakeGitHubClient) FetchProjectFields(ctx context.Context, owner, repo string, kind protocol.GitHubItemKind, number int) ([]protocol.GitHubProjectFieldValue, error) {
	f.projectFieldCalls++
	return nil, nil
}
