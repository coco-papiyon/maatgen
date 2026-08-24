// Package githubmonitor implements the ADR-007 monitoring business logic
// that sits between the GitHub adapter (internal/githubapi) and Outbox
// storage (internal/storage/sqlite): change detection against previously
// observed state, Trigger Rule condition evaluation, delivery key
// construction, and Prompt template rendering.
package githubmonitor

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// HashItem returns a stable hash over the GitHubItem fields that matter for
// change detection (ADR-007 section 6): title, body, state, author,
// assignees, labels, milestone, PR-specific fields (including requested
// reviewers), and Project field
// values. Equal items (regardless of field order in slices) always hash
// the same; any relevant field changing changes the hash.
//
// CreatedAt, URL, and Number never change once an item exists, so they are
// excluded. UpdatedAt is deliberately excluded too: ADR-007 section 6
// treats "the action", "the updated time", and "the state hash" as three
// independent signals combined by DeliveryKey, not folded into one another.
//
// Project field values are included: they are frequently the *only* thing
// that changes when, for example, an Issue's Status moves to "Ready" — the
// canonical trigger in ADR-007's motivating example — so they must
// participate in change detection like any other field. Whether Project
// data is available at all is also folded in, so a transition between
// "Projects unavailable" and "Projects fetched" is itself a detectable
// change and gets re-evaluated against rules.
func HashItem(item protocol.GitHubItem) string {
	var b strings.Builder
	b.WriteString(string(item.Kind))
	b.WriteByte('\n')
	b.WriteString(strconv.Itoa(item.Number))
	b.WriteByte('\n')
	b.WriteString(item.Title)
	b.WriteByte('\n')
	b.WriteString(item.Body)
	b.WriteByte('\n')
	b.WriteString(string(item.State))
	b.WriteByte('\n')
	b.WriteString(item.Author.Login)
	b.WriteByte('\n')
	writeSortedJoin(&b, userLogins(item.Assignees))
	writeSortedJoin(&b, labelNames(item.Labels))
	if item.Milestone != nil {
		b.WriteString(item.Milestone.Title)
	}
	b.WriteByte('\n')
	if item.PullRequest != nil {
		b.WriteString(strconv.FormatBool(item.PullRequest.Draft))
		b.WriteByte('\n')
		b.WriteString(item.PullRequest.Base.Ref)
		b.WriteByte('/')
		b.WriteString(item.PullRequest.Base.SHA)
		b.WriteByte('\n')
		b.WriteString(item.PullRequest.Head.Ref)
		b.WriteByte('/')
		b.WriteString(item.PullRequest.Head.SHA)
		b.WriteByte('\n')
		writeSortedJoin(&b, userLogins(item.PullRequest.RequestedReviewers))
	}
	if item.HasProjectData() {
		writeSortedJoin(&b, projectFieldEntries(item.ProjectFields))
	} else {
		b.WriteString("project-data-unavailable")
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func userLogins(users []protocol.GitHubUser) []string {
	logins := make([]string, len(users))
	for i, user := range users {
		logins[i] = user.Login
	}
	return logins
}

func labelNames(labels []protocol.GitHubLabel) []string {
	names := make([]string, len(labels))
	for i, label := range labels {
		names[i] = label.Name
	}
	return names
}

func projectFieldEntries(fields []protocol.GitHubProjectFieldValue) []string {
	entries := make([]string, len(fields))
	for i, field := range fields {
		entries[i] = field.ProjectTitle + "/" + field.FieldName + "=" + field.Value
	}
	return entries
}

func writeSortedJoin(b *strings.Builder, values []string) {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	b.WriteString(strings.Join(sorted, ","))
	b.WriteByte('\n')
}
