package githubmonitor

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// DeliveryKey builds the ADR-007 section 6 dedupe key for one rule and one
// GitHub item. Action, item state, and rule version are deliberately excluded:
// once a rule has produced an event for an item, later opened, updated, closed,
// or reopened observations of that same item must not produce another event.
func DeliveryKey(repository string, kind protocol.GitHubItemKind, number int, ruleID string) string {
	parts := []string{
		repository,
		string(kind),
		strconv.Itoa(number),
		ruleID,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
