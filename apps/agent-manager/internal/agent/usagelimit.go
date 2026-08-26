package agent

import "strings"

// usageLimitPhrases are case-insensitive substrings that indicate a
// provider CLI stopped a Run because an account-level usage or rate limit
// was reached, rather than because of an ordinary task failure. The list is
// intentionally broad across providers (Claude Code, Codex, GitHub Copilot)
// because none of them expose a machine-readable reason code for this
// specific case in every failure shape (see ADR-008).
//
// "limit reached" alone is deliberately excluded: Claude Code also reports
// an unrelated "turn limit reached" (subtype error_max_turns) when a single
// turn's tool-call budget is exhausted, which is not a usage/session limit
// and must not trigger an automatic retry.
var usageLimitPhrases = []string{
	"usage limit",
	"session limit",
	"you've hit your",
	"you have hit your",
	"rate limit exceeded",
	"quota exceeded",
}

// LooksLikeUsageLimitMessage reports whether a line of CLI output describes
// an account-level usage or rate limit being reached.
func LooksLikeUsageLimitMessage(line string) bool {
	lower := strings.ToLower(line)
	for _, phrase := range usageLimitPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}
