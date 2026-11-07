package githubmonitor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
)

func TestPollerClosesExistingJobsForClosedItems(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	monitor := testRepositoryMonitor("/repo", nil)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	client := &fakeGitHubClient{
		getIssues:       map[int]protocol.GitHubItem{1: {State: protocol.GitHubItemClosed}, 2: {State: protocol.GitHubItemOpen}, 3: {State: protocol.GitHubItemClosed}},
		getIssueErr:     map[int]error{3: errors.New("API unavailable")},
		getPullRequests: map[int]protocol.GitHubItem{1: {State: protocol.GitHubItemClosed}},
	}
	poller := NewPoller(store, NewEvaluator(store), func(string) (GitHubClient, error) { return client, nil }, unchangedRemoteResolver)
	active := true
	poller.SetEventCloser(func(ctx context.Context, id string) (protocol.GitHubMonitorEvent, error) {
		if id == "active" && active {
			return protocol.GitHubMonitorEvent{}, session.ErrRunActive
		}
		if err := store.CloseMonitorEvent(ctx, id, time.Now()); err != nil {
			return protocol.GitHubMonitorEvent{}, err
		}
		return store.GetMonitorEvent(ctx, id)
	})
	add := func(id string, kind protocol.GitHubItemKind, number int, status protocol.GitHubMonitorEventStatus, url string) {
		t.Helper()
		path := "issues"
		if kind == protocol.GitHubItemPullRequest {
			path = "pull"
		}
		if url == "" {
			url = fmt.Sprintf("https://github.com/octo-org/example/%s/%d", path, number)
		}
		_, err := store.InsertMonitorEvent(ctx, protocol.GitHubMonitorEvent{
			ID: id, Repository: monitor.Repository, Kind: kind, Number: number, Status: status,
			ItemSnapshot: protocol.GitHubItem{Kind: kind, Number: number, URL: url},
			CreatedAt:    time.Now(), UpdatedAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// More than the UI's default limit, including multiple rules/replays of one item.
	for i := 0; i < 105; i++ {
		add(fmt.Sprintf("issue-%d", i), protocol.GitHubItemIssue, 1, protocol.GitHubMonitorEventQueued, "")
	}
	add("pr", protocol.GitHubItemPullRequest, 1, protocol.GitHubMonitorEventCompleted, "")
	add("active", protocol.GitHubItemIssue, 1, protocol.GitHubMonitorEventRunStarted, "")
	add("open", protocol.GitHubItemIssue, 2, protocol.GitHubMonitorEventSkipped, "")
	add("failed-fetch", protocol.GitHubItemIssue, 3, protocol.GitHubMonitorEventFailed, "")
	add("old-remote", protocol.GitHubItemIssue, 1, protocol.GitHubMonitorEventQueued, "https://github.com/other/repo/issues/1")
	add("already-closed", protocol.GitHubItemIssue, 1, protocol.GitHubMonitorEventClosed, "")
	if _, err := poller.SyncRepository(ctx, monitor.Repository); err == nil {
		t.Fatal("expected fetch failure to be reported")
	}
	assertStatus := func(id string, want protocol.GitHubMonitorEventStatus) {
		t.Helper()
		event, err := store.GetMonitorEvent(ctx, id)
		if err != nil || event.Status != want {
			t.Fatalf("%s = %s, err %v; want %s", id, event.Status, err, want)
		}
		if want == protocol.GitHubMonitorEventClosed && id != "already-closed" && event.ClosedAt == nil {
			t.Fatalf("%s missing closedAt", id)
		}
	}
	for i := 0; i < 105; i++ {
		assertStatus(fmt.Sprintf("issue-%d", i), protocol.GitHubMonitorEventClosed)
	}
	assertStatus("pr", protocol.GitHubMonitorEventClosed)
	assertStatus("active", protocol.GitHubMonitorEventRunStarted)
	assertStatus("open", protocol.GitHubMonitorEventSkipped)
	assertStatus("failed-fetch", protocol.GitHubMonitorEventFailed)
	assertStatus("old-remote", protocol.GitHubMonitorEventQueued)
	if client.issueFetches != 3 || client.pullFetches != 1 {
		t.Fatalf("detail calls issue=%d pull=%d", client.issueFetches, client.pullFetches)
	}
	delete(client.getIssueErr, 3)
	if _, err := poller.SyncRepository(ctx, monitor.Repository); err != nil {
		t.Fatal(err)
	}
	assertStatus("active", protocol.GitHubMonitorEventRunStarted)
	assertStatus("failed-fetch", protocol.GitHubMonitorEventClosed)
	updated, err := store.GetRepositoryMonitor(ctx, monitor.Repository)
	if err != nil || updated.LastError != nil {
		t.Fatalf("active Run must defer closure without failing sync: monitor=%#v err=%v", updated, err)
	}
	active = false
	if _, err := poller.SyncRepository(ctx, monitor.Repository); err != nil {
		t.Fatal(err)
	}
	assertStatus("active", protocol.GitHubMonitorEventClosed)
	all, err := store.ListMonitorEvents(ctx, monitor.Repository, -1, "all")
	if err != nil || len(all) != 111 {
		t.Fatalf("unexpected new jobs: count=%d err=%v", len(all), err)
	}
}

func TestPollerDoesNotFetchOpenJobDetailsForClosure(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	monitor := testRepositoryMonitor("/repo", nil)
	if err := store.CreateRepositoryMonitor(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	item := protocol.GitHubItem{Kind: protocol.GitHubItemIssue, Number: 1, State: protocol.GitHubItemOpen, URL: "https://github.com/octo-org/example/issues/1"}
	if _, err := store.InsertMonitorEvent(ctx, protocol.GitHubMonitorEvent{ID: "open", Repository: monitor.Repository, Kind: item.Kind, Number: item.Number, Status: protocol.GitHubMonitorEventQueued, ItemSnapshot: item, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	client := &fakeGitHubClient{issues: []protocol.GitHubItem{item}}
	poller := NewPoller(store, NewEvaluator(store), func(string) (GitHubClient, error) { return client, nil }, unchangedRemoteResolver)
	poller.SetEventCloser(func(context.Context, string) (protocol.GitHubMonitorEvent, error) {
		t.Fatal("open job must not close")
		return protocol.GitHubMonitorEvent{}, nil
	})
	if _, err := poller.SyncRepository(ctx, monitor.Repository); err != nil {
		t.Fatal(err)
	}
	if client.issueFetches != 0 {
		t.Fatal("open list item fetched again")
	}
}
