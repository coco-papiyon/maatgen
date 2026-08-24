package githubmonitor

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// DeliveryKey builds the ADR-007 section 6 dedupe key for one rule's
// evaluation of one detected change: repository + item kind + number +
// "event identity" + rule ID. GitHub event IDs are not available (no
// webhooks; ADR-007 section 2 scopes this out), so "event identity" is
// derived from the action, the item's updated time, and the new state
// hash — the combination ADR-007 section 6 specifies for this exact case.
// The same detected change evaluated against the same rule always yields
// the same key, so re-evaluating it (e.g. after a crash/restart before the
// observed baseline advanced) can never insert a second Outbox event.
func DeliveryKey(repository string, kind protocol.GitHubItemKind, number int, action string, updatedAt time.Time, stateHash, ruleID string) string {
	parts := []string{
		repository,
		string(kind),
		strconv.Itoa(number),
		action,
		updatedAt.UTC().Format(time.RFC3339Nano),
		stateHash,
		ruleID,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
