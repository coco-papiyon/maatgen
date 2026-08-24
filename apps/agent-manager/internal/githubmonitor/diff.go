package githubmonitor

import "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"

// Change describes what a freshly fetched GitHubItem looks like relative to
// the last observed state of the same item (ADR-007 section 3 and 6).
type Change struct {
	// StateHash is HashItem(item), the new baseline to store.
	StateHash string
	// Action is one of "opened", "updated", "closed", "reopened". It is
	// only meaningful when Changed is true.
	Action string
	// Changed is false when the item is byte-for-byte equivalent (per
	// HashItem) to the last observed state: nothing to evaluate rules
	// against.
	Changed bool
	// BeforeStateHash is the previous StateHash, or nil if this item has
	// never been observed before.
	BeforeStateHash *string
}

// DetectChange compares a freshly fetched item against its previously
// observed state. previous is nil when the item has never been observed
// for this repository before.
//
// Note: DetectChange does not know whether this is the monitor's first-ever
// poll cycle, so it always reports "opened" for a never-before-seen item —
// callers must separately suppress rule evaluation during the monitor's
// first sync (ADR-007 section 3: "初回同期は観測基準点を作るだけ"), since
// on that cycle every fetched item is new to us.
func DetectChange(previous *protocol.GitHubObservedItem, item protocol.GitHubItem) Change {
	hash := HashItem(item)
	if previous == nil {
		return Change{StateHash: hash, Action: "opened", Changed: true}
	}
	if previous.StateHash == hash {
		return Change{StateHash: hash, Changed: false, BeforeStateHash: &previous.StateHash}
	}
	action := "updated"
	switch {
	case previous.Snapshot.State == protocol.GitHubItemOpen && item.State == protocol.GitHubItemClosed:
		action = "closed"
	case previous.Snapshot.State == protocol.GitHubItemClosed && item.State == protocol.GitHubItemOpen:
		action = "reopened"
	}
	before := previous.StateHash
	return Change{StateHash: hash, Action: action, Changed: true, BeforeStateHash: &before}
}
