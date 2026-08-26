package githubapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v69/github"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode json response: %v", err)
	}
}

func newTestRESTClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	rest := github.NewClient(server.Client())
	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	rest.BaseURL = baseURL
	return &Client{rest: rest, Host: "github.com"}
}

func TestListIssuesExcludesPullRequestsAndPaginates(t *testing.T) {
	pages := [][]map[string]any{
		{
			{"number": 1, "title": "first issue", "state": "open"},
			{"number": 2, "title": "a pull request", "state": "open", "pull_request": map[string]any{"url": "x"}},
		},
		{
			{"number": 3, "title": "second issue", "state": "closed"},
		},
	}
	requestedPages := 0
	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/octo-org/example/issues" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		page := r.URL.Query().Get("page")
		index := 0
		if page == "2" {
			index = 1
		}
		requestedPages++
		if index == 0 {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, "http://"+r.Host+"/repos/octo-org/example/issues"))
		}
		writeJSON(t, w, pages[index])
	})

	items, err := client.ListIssues(context.Background(), "octo-org", "example", ListOptions{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if requestedPages != 2 {
		t.Fatalf("requestedPages = %d, want 2", requestedPages)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v, want 2 issues (pull request excluded)", items)
	}
	if items[0].Number != 1 || items[0].Kind != protocol.GitHubItemIssue {
		t.Fatalf("items[0] = %#v", items[0])
	}
	if items[1].Number != 3 || items[1].State != protocol.GitHubItemClosed {
		t.Fatalf("items[1] = %#v", items[1])
	}
}

func TestListIssuesAndPullRequestsLogFetches(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/octo-org/example/issues":
			writeJSON(t, w, []map[string]any{{"number": 1, "title": "issue", "state": "open"}})
		case "/repos/octo-org/example/pulls":
			writeJSON(t, w, []map[string]any{{"number": 2, "title": "pull", "state": "open"}})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	})

	if _, err := client.ListIssues(context.Background(), "octo-org", "example", ListOptions{State: "open"}); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if _, err := client.ListPullRequests(context.Background(), "octo-org", "example", ListOptions{State: "open"}); err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}

	output := logs.String()
	for _, expected := range []string{
		"github issue list fetch started",
		"github issue list fetch completed",
		"github pull request list fetch started",
		"github pull request list fetch completed",
		"owner=octo-org",
		"repository=example",
		"count=1",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("log output %q does not contain %q", output, expected)
		}
	}
}

func TestGetIssueNormalizesFields(t *testing.T) {
	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/octo-org/example/issues/42" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"number":     42,
			"title":      "Something is broken",
			"body":       "details",
			"state":      "open",
			"html_url":   "https://github.com/octo-org/example/issues/42",
			"user":       map[string]any{"login": "alice"},
			"assignees":  []map[string]any{{"login": "bob"}},
			"labels":     []map[string]any{{"name": "bug"}},
			"milestone":  map[string]any{"title": "v1.0"},
			"created_at": "2026-01-01T00:00:00Z",
			"updated_at": "2026-01-02T00:00:00Z",
		})
	})

	item, err := client.GetIssue(context.Background(), "octo-org", "example", 42)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if item.Kind != protocol.GitHubItemIssue || item.Number != 42 || item.Title != "Something is broken" {
		t.Fatalf("item = %#v", item)
	}
	if item.Author.Login != "alice" || len(item.Assignees) != 1 || item.Assignees[0].Login != "bob" {
		t.Fatalf("item = %#v", item)
	}
	if len(item.Labels) != 1 || item.Labels[0].Name != "bug" {
		t.Fatalf("item.Labels = %#v", item.Labels)
	}
	if item.Milestone == nil || item.Milestone.Title != "v1.0" {
		t.Fatalf("item.Milestone = %#v", item.Milestone)
	}
	if item.URL != "https://github.com/octo-org/example/issues/42" {
		t.Fatalf("item.URL = %q", item.URL)
	}
}

func TestGetPullRequestNormalizesFields(t *testing.T) {
	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/octo-org/example/pulls/7" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"number":              7,
			"title":               "Add feature",
			"state":               "open",
			"draft":               true,
			"html_url":            "https://github.com/octo-org/example/pull/7",
			"user":                map[string]any{"login": "carol"},
			"requested_reviewers": []map[string]any{{"login": "reviewer-one"}},
			"base":                map[string]any{"ref": "main", "sha": "aaa"},
			"head":                map[string]any{"ref": "feature", "sha": "bbb"},
		})
	})

	item, err := client.GetPullRequest(context.Background(), "octo-org", "example", 7)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if item.Kind != protocol.GitHubItemPullRequest {
		t.Fatalf("item.Kind = %v", item.Kind)
	}
	if item.PullRequest == nil || !item.PullRequest.Draft {
		t.Fatalf("item.PullRequest = %#v", item.PullRequest)
	}
	if item.PullRequest.Base.Ref != "main" || item.PullRequest.Head.Ref != "feature" {
		t.Fatalf("item.PullRequest = %#v", item.PullRequest)
	}
	if len(item.PullRequest.RequestedReviewers) != 1 || item.PullRequest.RequestedReviewers[0].Login != "reviewer-one" {
		t.Fatalf("item.PullRequest.RequestedReviewers = %#v", item.PullRequest.RequestedReviewers)
	}
}

func TestGetPullRequestNormalizesConflictState(t *testing.T) {
	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number":          8,
			"title":           "Add feature",
			"state":           "open",
			"html_url":        "https://github.com/octo-org/example/pull/8",
			"user":            map[string]any{"login": "carol"},
			"base":            map[string]any{"ref": "main", "sha": "aaa"},
			"head":            map[string]any{"ref": "feature", "sha": "bbb"},
			"mergeable_state": "dirty",
		})
	})

	item, err := client.GetPullRequest(context.Background(), "octo-org", "example", 8)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if item.PullRequest == nil || !item.PullRequest.Conflicting {
		t.Fatalf("item.PullRequest = %#v, want Conflicting=true for mergeable_state=dirty", item.PullRequest)
	}
}

func TestGetPullRequestTreatsUnknownMergeableStateAsNoConflict(t *testing.T) {
	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"number":          9,
			"title":           "Add feature",
			"state":           "open",
			"html_url":        "https://github.com/octo-org/example/pull/9",
			"user":            map[string]any{"login": "carol"},
			"base":            map[string]any{"ref": "main", "sha": "aaa"},
			"head":            map[string]any{"ref": "feature", "sha": "bbb"},
			"mergeable_state": "unknown",
		})
	})

	item, err := client.GetPullRequest(context.Background(), "octo-org", "example", 9)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if item.PullRequest == nil || item.PullRequest.Conflicting {
		t.Fatalf("item.PullRequest = %#v, want Conflicting=false when GitHub has not determined mergeability yet", item.PullRequest)
	}
}

func TestListIssuesWrapsError(t *testing.T) {
	client := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(t, w, map[string]any{"message": "Not Found"})
	})
	if _, err := client.ListIssues(context.Background(), "octo-org", "example", ListOptions{}); err == nil {
		t.Fatalf("expected error")
	} else if !IsNotFound(err) {
		t.Fatalf("err = %v, want IsNotFound", err)
	}
}
