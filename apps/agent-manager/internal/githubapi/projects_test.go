package githubapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shurcooL/githubv4"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func newTestGraphQLClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Client{graphql: githubv4.NewEnterpriseClient(server.URL, server.Client()), Host: "github.com"}
}

func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return string(data)
}

func TestFetchProjectFieldsIssue(t *testing.T) {
	client := newTestGraphQLClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readBody(t, r)
		if !strings.Contains(body, "issue(number:") {
			t.Fatalf("expected an issue query, got %s", body)
		}
		writeJSON(t, w, map[string]any{
			"data": map[string]any{
				"repository": map[string]any{
					"issue": map[string]any{
						"projectItems": map[string]any{
							"nodes": []any{
								map[string]any{
									"project": map[string]any{"title": "Roadmap"},
									"fieldValues": map[string]any{
										"nodes": []any{
											map[string]any{
												"__typename": "ProjectV2ItemFieldSingleSelectValue",
												"name":       "Ready",
												"field":      map[string]any{"name": "Status"},
											},
											map[string]any{
												"__typename": "ProjectV2ItemFieldTextValue",
												"text":       "some notes",
												"field":      map[string]any{"name": "Notes"},
											},
											map[string]any{
												// A field type not modeled (e.g. iteration) must be
												// skipped, not crash decoding.
												"__typename": "ProjectV2ItemFieldRepositoryValue",
												"field":      map[string]any{"name": "Repo"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		})
	})

	fields, err := client.FetchProjectFields(context.Background(), "octo-org", "example", protocol.GitHubItemIssue, 42)
	if err != nil {
		t.Fatalf("FetchProjectFields: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("fields = %#v, want 2", fields)
	}
	if fields[0].ProjectTitle != "Roadmap" || fields[0].FieldName != "Status" || fields[0].Value != "Ready" {
		t.Fatalf("fields[0] = %#v", fields[0])
	}
	if fields[1].FieldName != "Notes" || fields[1].Value != "some notes" {
		t.Fatalf("fields[1] = %#v", fields[1])
	}
}

func TestFetchProjectFieldsPullRequest(t *testing.T) {
	client := newTestGraphQLClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := readBody(t, r)
		if !strings.Contains(body, "pullRequest(number:") {
			t.Fatalf("expected a pull request query, got %s", body)
		}
		writeJSON(t, w, map[string]any{
			"data": map[string]any{
				"repository": map[string]any{
					"pullRequest": map[string]any{
						"projectItems": map[string]any{
							"nodes": []any{},
						},
					},
				},
			},
		})
	})

	fields, err := client.FetchProjectFields(context.Background(), "octo-org", "example", protocol.GitHubItemPullRequest, 7)
	if err != nil {
		t.Fatalf("FetchProjectFields: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("fields = %#v, want empty", fields)
	}
}

func TestFetchProjectFieldsGraphQLError(t *testing.T) {
	client := newTestGraphQLClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"errors": []any{map[string]any{"message": "Resource not accessible by integration"}},
		})
	})

	if _, err := client.FetchProjectFields(context.Background(), "octo-org", "example", protocol.GitHubItemIssue, 42); err == nil {
		t.Fatalf("expected error")
	}
}

func TestFetchItemRecordsProjectsErrorWithoutFailing(t *testing.T) {
	restCalled := false
	graphqlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"errors": []any{map[string]any{"message": "Projects not available"}},
		})
	}))
	t.Cleanup(graphqlServer.Close)

	restClient := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		restCalled = true
		writeJSON(t, w, map[string]any{"number": 42, "title": "t", "state": "open"})
	})
	client := &Client{
		rest:    restClient.rest,
		graphql: githubv4.NewEnterpriseClient(graphqlServer.URL, graphqlServer.Client()),
		Host:    "github.com",
	}

	item, err := client.FetchItem(context.Background(), "octo-org", "example", protocol.GitHubItemIssue, 42)
	if err != nil {
		t.Fatalf("FetchItem returned an error, want Projects failure recorded instead: %v", err)
	}
	if !restCalled {
		t.Fatalf("expected the REST issue fetch to run")
	}
	if item.ProjectsError == "" {
		t.Fatalf("expected ProjectsError to be set")
	}
	if item.HasProjectData() {
		t.Fatalf("HasProjectData() = true, want false when ProjectsError is set")
	}
}

func TestFetchItemSuccessHasNonNilProjectFields(t *testing.T) {
	restClient := newTestRESTClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"number": 42, "title": "t", "state": "open"})
	})
	graphqlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": map[string]any{
				"repository": map[string]any{
					"issue": map[string]any{
						"projectItems": map[string]any{"nodes": []any{}},
					},
				},
			},
		})
	}))
	t.Cleanup(graphqlServer.Close)
	client := &Client{
		rest:    restClient.rest,
		graphql: githubv4.NewEnterpriseClient(graphqlServer.URL, graphqlServer.Client()),
		Host:    "github.com",
	}

	item, err := client.FetchItem(context.Background(), "octo-org", "example", protocol.GitHubItemIssue, 42)
	if err != nil {
		t.Fatalf("FetchItem: %v", err)
	}
	if item.ProjectsError != "" {
		t.Fatalf("ProjectsError = %q, want empty", item.ProjectsError)
	}
	if !item.HasProjectData() {
		t.Fatalf("HasProjectData() = false, want true after a successful (even empty) fetch")
	}
}
