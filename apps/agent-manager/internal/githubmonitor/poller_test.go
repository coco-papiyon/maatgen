package githubmonitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubapi"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type fakeGitHubClient struct {
	issues        []protocol.GitHubItem
	pulls         []protocol.GitHubItem
	projectFields map[int][]protocol.GitHubProjectFieldValue
	projectErr    map[int]error
	listIssuesErr error
	listPullsErr  error
}

func (f *fakeGitHubClient) ListIssues(ctx context.Context, owner, repo string, opts githubapi.ListOptions) ([]protocol.GitHubItem, error) {
	if f.listIssuesErr != nil {
		return nil, f.listIssuesErr
	}
	return f.issues, nil
}

func (f *fakeGitHubClient) ListPullRequests(ctx context.Context, owner, repo string, opts githubapi.ListOptions) ([]protocol.GitHubItem, error) {
	if f.listPullsErr != nil {
		return nil, f.listPullsErr
	}
	return f.pulls, nil
}

func (f *fakeGitHubClient) FetchProjectFields(ctx context.Context, owner, repo string, kind protocol.GitHubItemKind, number int) ([]protocol.GitHubProjectFieldValue, error) {
	if f.projectErr != nil {
		if err, ok := f.projectErr[number]; ok {
			return nil, err
		}
	}
	if f.projectFields == nil {
		return nil, nil
	}
	return f.projectFields[number], nil
}

// unchangedRemoteResolver simulates a RemoteResolveFunc that always finds
// the monitor's already-configured remote unchanged, matching how these
// tests' fixture monitor (host github.com, owner octo-org, name example,
// remote origin) is set up.
func unchangedRemoteResolver(ctx context.Context, repository string, remoteName string) (*RemoteCandidate, error) {
	return &RemoteCandidate{Host: "github.com", Owner: "octo-org", Name: "example", RemoteName: "origin"}, nil
}

func TestPollerSyncRepositoryFirstSyncEstablishesBaselineWithoutFiring(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "/repo"
	monitor := testRepositoryMonitor(repository, nil) // first-ever sync
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	createReadyRule(t, store, repository)

	client := &fakeGitHubClient{
		issues: []protocol.GitHubItem{{
			Kind: protocol.GitHubItemIssue, Number: 1, Title: "Something", State: protocol.GitHubItemOpen,
		}},
		projectFields: map[int][]protocol.GitHubProjectFieldValue{
			1: {{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}},
		},
	}
	poller := NewPoller(store, NewEvaluator(store), func(string) (GitHubClient, error) { return client, nil }, unchangedRemoteResolver)

	result, err := poller.SyncRepository(ctx, repository)
	if err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if result.IssuesProcessed != 1 || result.EventsMatched != 0 {
		t.Fatalf("result = %#v, want 1 issue processed and 0 events (first sync)", result)
	}

	updated, err := store.GetRepositoryMonitor(ctx, repository)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.LastSyncedAt == nil || updated.LastError != nil {
		t.Fatalf("monitor = %#v", updated)
	}
	observed, err := store.GetObservedItem(ctx, repository, protocol.GitHubItemIssue, 1)
	if err != nil {
		t.Fatalf("expected an observed baseline: %v", err)
	}
	if !observed.ProjectsAvailable {
		t.Fatalf("observed.ProjectsAvailable = false, want true")
	}
}

func TestPollerSyncRepositoryFiresOnceStatusBecomesReady(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "/repo"
	syncedAt := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	monitor := testRepositoryMonitor(repository, &syncedAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	createReadyRule(t, store, repository)

	client := &fakeGitHubClient{
		issues: []protocol.GitHubItem{{
			Kind: protocol.GitHubItemIssue, Number: 1, Title: "Something", State: protocol.GitHubItemOpen,
		}},
		projectFields: map[int][]protocol.GitHubProjectFieldValue{
			1: {{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}},
		},
	}
	poller := NewPoller(store, NewEvaluator(store), func(string) (GitHubClient, error) { return client, nil }, unchangedRemoteResolver)

	result, err := poller.SyncRepository(ctx, repository)
	if err != nil {
		t.Fatalf("SyncRepository: %v", err)
	}
	if result.EventsMatched != 1 {
		t.Fatalf("result = %#v, want 1 event matched", result)
	}

	events, err := store.ListMonitorEventsByStatus(ctx, protocol.GitHubMonitorEventQueued, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Number != 1 {
		t.Fatalf("events = %#v", events)
	}
}

func TestPollerSyncRepositoryRecordsProjectsErrorWithoutFailingSync(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "/repo"
	syncedAt := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	monitor := testRepositoryMonitor(repository, &syncedAt)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	client := &fakeGitHubClient{
		issues: []protocol.GitHubItem{{
			Kind: protocol.GitHubItemIssue, Number: 1, Title: "Something", State: protocol.GitHubItemOpen,
		}},
		projectErr: map[int]error{1: errors.New("insufficient permission")},
	}
	poller := NewPoller(store, NewEvaluator(store), func(string) (GitHubClient, error) { return client, nil }, unchangedRemoteResolver)

	result, err := poller.SyncRepository(ctx, repository)
	if err != nil {
		t.Fatalf("SyncRepository must not fail when only Projects fetch fails: %v", err)
	}
	if result.IssuesProcessed != 1 {
		t.Fatalf("result = %#v", result)
	}
	updated, err := store.GetRepositoryMonitor(ctx, repository)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.LastError != nil {
		t.Fatalf("monitor.LastError = %#v, want nil: a Projects failure must not surface as a sync failure", updated.LastError)
	}
	observed, err := store.GetObservedItem(ctx, repository, protocol.GitHubItemIssue, 1)
	if err != nil {
		t.Fatalf("get observed item: %v", err)
	}
	if observed.ProjectsAvailable {
		t.Fatalf("observed.ProjectsAvailable = true, want false")
	}
}

func TestPollerSyncRepositoryRecordsListFailure(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	repository := "/repo"
	monitor := testRepositoryMonitor(repository, nil)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatalf("create monitor: %v", err)
	}

	client := &fakeGitHubClient{listIssuesErr: errors.New("network unreachable")}
	poller := NewPoller(store, NewEvaluator(store), func(string) (GitHubClient, error) { return client, nil }, unchangedRemoteResolver)

	if _, err := poller.SyncRepository(ctx, repository); err == nil {
		t.Fatalf("expected an error")
	}
	updated, err := store.GetRepositoryMonitor(ctx, repository)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	if updated.LastError == nil || *updated.LastError == "" {
		t.Fatalf("expected LastError to be recorded, got %#v", updated.LastError)
	}
	if updated.NextSyncAt == nil {
		t.Fatalf("expected NextSyncAt to be scheduled even after a failure")
	}
}

func TestPollerSyncRepositoryUnknownRepositoryReturnsError(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	poller := NewPoller(store, NewEvaluator(store), func(string) (GitHubClient, error) { return &fakeGitHubClient{}, nil }, unchangedRemoteResolver)
	if _, err := poller.SyncRepository(ctx, "/missing"); err == nil {
		t.Fatalf("expected an error for an unregistered repository")
	}
}
