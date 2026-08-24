package githubmonitor

import (
	"testing"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestDetectChangeNoPreviousIsOpened(t *testing.T) {
	item := baseItem()
	change := DetectChange(nil, item)
	if !change.Changed || change.Action != "opened" {
		t.Fatalf("change = %#v", change)
	}
	if change.BeforeStateHash != nil {
		t.Fatalf("BeforeStateHash = %#v, want nil", change.BeforeStateHash)
	}
	if change.StateHash != HashItem(item) {
		t.Fatalf("StateHash mismatch")
	}
}

func TestDetectChangeUnchanged(t *testing.T) {
	item := baseItem()
	previous := protocol.GitHubObservedItem{StateHash: HashItem(item), Snapshot: item}
	change := DetectChange(&previous, item)
	if change.Changed {
		t.Fatalf("expected no change, got %#v", change)
	}
}

func TestDetectChangeClosedAndReopened(t *testing.T) {
	openItem := baseItem()
	closedItem := baseItem()
	closedItem.State = protocol.GitHubItemClosed

	previousOpen := protocol.GitHubObservedItem{StateHash: HashItem(openItem), Snapshot: openItem}
	closed := DetectChange(&previousOpen, closedItem)
	if !closed.Changed || closed.Action != "closed" {
		t.Fatalf("closed change = %#v", closed)
	}
	if closed.BeforeStateHash == nil || *closed.BeforeStateHash != previousOpen.StateHash {
		t.Fatalf("BeforeStateHash = %#v", closed.BeforeStateHash)
	}

	previousClosed := protocol.GitHubObservedItem{StateHash: HashItem(closedItem), Snapshot: closedItem}
	reopened := DetectChange(&previousClosed, openItem)
	if !reopened.Changed || reopened.Action != "reopened" {
		t.Fatalf("reopened change = %#v", reopened)
	}
}

func TestDetectChangeOtherFieldChangeIsUpdated(t *testing.T) {
	before := baseItem()
	after := baseItem()
	after.Title = "New title"
	previous := protocol.GitHubObservedItem{StateHash: HashItem(before), Snapshot: before}
	change := DetectChange(&previous, after)
	if !change.Changed || change.Action != "updated" {
		t.Fatalf("change = %#v", change)
	}
}

func TestDetectChangeProjectStatusChangeIsUpdated(t *testing.T) {
	before := baseItem()
	before.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Todo"}}
	after := baseItem()
	after.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}}
	previous := protocol.GitHubObservedItem{StateHash: HashItem(before), Snapshot: before}
	change := DetectChange(&previous, after)
	if !change.Changed || change.Action != "updated" {
		t.Fatalf("a Project-only change must still be detected: %#v", change)
	}
}
