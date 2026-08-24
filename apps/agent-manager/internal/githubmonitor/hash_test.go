package githubmonitor

import (
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func baseItem() protocol.GitHubItem {
	return protocol.GitHubItem{
		Kind:      protocol.GitHubItemIssue,
		Number:    1,
		Title:     "Something is broken",
		Body:      "details",
		State:     protocol.GitHubItemOpen,
		Author:    protocol.GitHubUser{Login: "alice"},
		Assignees: []protocol.GitHubUser{{Login: "bob"}},
		Labels:    []protocol.GitHubLabel{{Name: "bug"}},
	}
}

func TestHashItemStableForEquivalentInput(t *testing.T) {
	a := baseItem()
	b := baseItem()
	if HashItem(a) != HashItem(b) {
		t.Fatalf("identical items produced different hashes")
	}
}

func TestHashItemIgnoresSliceOrder(t *testing.T) {
	a := baseItem()
	a.Assignees = []protocol.GitHubUser{{Login: "bob"}, {Login: "carol"}}
	a.Labels = []protocol.GitHubLabel{{Name: "bug"}, {Name: "P1"}}
	b := baseItem()
	b.Assignees = []protocol.GitHubUser{{Login: "carol"}, {Login: "bob"}}
	b.Labels = []protocol.GitHubLabel{{Name: "P1"}, {Name: "bug"}}
	if HashItem(a) != HashItem(b) {
		t.Fatalf("hash should not depend on slice order")
	}
}

func TestHashItemChangesWithTitle(t *testing.T) {
	a := baseItem()
	b := baseItem()
	b.Title = "Different title"
	if HashItem(a) == HashItem(b) {
		t.Fatalf("hash should change when title changes")
	}
}

func TestHashItemChangesWithProjectFields(t *testing.T) {
	a := baseItem()
	a.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Todo"}}
	b := baseItem()
	b.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}}
	if HashItem(a) == HashItem(b) {
		t.Fatalf("hash must change when a Project field value changes (the canonical ADR-007 trigger)")
	}
}

func TestHashItemDistinguishesProjectDataUnavailableFromEmpty(t *testing.T) {
	unavailable := baseItem()
	unavailable.ProjectsError = "graphql error"

	fetchedEmpty := baseItem()
	fetchedEmpty.ProjectFields = []protocol.GitHubProjectFieldValue{}

	if HashItem(unavailable) == HashItem(fetchedEmpty) {
		t.Fatalf("hash must distinguish 'Projects unavailable' from 'Projects fetched, no entries', so recovery is detected as a change")
	}
}

func TestHashItemIgnoresUpdatedAt(t *testing.T) {
	a := baseItem()
	b := baseItem()
	b.UpdatedAt = a.UpdatedAt.Add(1000000)
	if HashItem(a) != HashItem(b) {
		t.Fatalf("UpdatedAt must not participate in the content hash (ADR-007 section 6 treats it as an independent signal)")
	}
}
