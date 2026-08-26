package agent

import "testing"

func TestLooksLikeUsageLimitMessage(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"You've hit your session limit. Try again later.", true},
		{"Claude AI usage limit reached|1700000000", true},
		{"error: rate limit exceeded, please retry", true},
		{"Quota exceeded for this billing period", true},
		{"YOU HAVE HIT YOUR USAGE LIMIT", true},
		{"turn limit reached", false},
		{"command failed: exit status 1", false},
		{"", false},
		{"file not found", false},
	}
	for _, tc := range cases {
		if got := LooksLikeUsageLimitMessage(tc.line); got != tc.want {
			t.Errorf("LooksLikeUsageLimitMessage(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}
