package protocol

import "testing"

func TestGitHubItemHasProjectData(t *testing.T) {
	cases := []struct {
		name string
		item GitHubItem
		want bool
	}{
		{"not fetched", GitHubItem{}, false},
		{"fetch failed", GitHubItem{ProjectsError: "graphql error"}, false},
		{"fetched empty", GitHubItem{ProjectFields: []GitHubProjectFieldValue{}}, true},
		{"fetched with data", GitHubItem{ProjectFields: []GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.item.HasProjectData(); got != tc.want {
				t.Fatalf("HasProjectData() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGitHubItemProjectFieldValue(t *testing.T) {
	item := GitHubItem{
		ProjectFields: []GitHubProjectFieldValue{
			{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"},
		},
	}

	if value, ok := item.ProjectFieldValue("roadmap", "status"); !ok || value != "Ready" {
		t.Fatalf("ProjectFieldValue case-insensitive lookup = (%q, %v), want (Ready, true)", value, ok)
	}
	if _, ok := item.ProjectFieldValue("Roadmap", "Priority"); ok {
		t.Fatalf("expected no match for unknown field")
	}
	if _, ok := (GitHubItem{}).ProjectFieldValue("Roadmap", "Status"); ok {
		t.Fatalf("expected no match when ProjectFields is nil")
	}
}
