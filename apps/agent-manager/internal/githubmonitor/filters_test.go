package githubmonitor

import (
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestMatchesEmptyFiltersAlwaysMatch(t *testing.T) {
	if !Matches(protocol.GitHubMonitorFilters{}, baseItem(), "opened") {
		t.Fatalf("empty filters should match everything")
	}
}

func TestMatchesLabelsIsOrWithinDimension(t *testing.T) {
	item := baseItem() // has label "bug"
	if !Matches(protocol.GitHubMonitorFilters{Labels: []string{"bug", "enhancement"}}, item, "opened") {
		t.Fatalf("expected match: item has one of the listed labels")
	}
	if Matches(protocol.GitHubMonitorFilters{Labels: []string{"enhancement", "wontfix"}}, item, "opened") {
		t.Fatalf("expected no match: item has none of the listed labels")
	}
}

func TestMatchesActionsAndCaseInsensitiveAuthors(t *testing.T) {
	item := baseItem() // author "alice"
	if !Matches(protocol.GitHubMonitorFilters{Actions: []string{"opened", "reopened"}, Authors: []string{"Alice"}}, item, "opened") {
		t.Fatalf("expected match")
	}
	if Matches(protocol.GitHubMonitorFilters{Actions: []string{"closed"}}, item, "opened") {
		t.Fatalf("expected no match on action")
	}
}

func TestMatchesTitleAndBodyContainsCaseInsensitive(t *testing.T) {
	item := baseItem() // Title: "Something is broken", Body: "details"
	title := "SOMETHING"
	if !Matches(protocol.GitHubMonitorFilters{TitleContains: &title}, item, "opened") {
		t.Fatalf("expected case-insensitive title match")
	}
	body := "missing"
	if Matches(protocol.GitHubMonitorFilters{BodyContains: &body}, item, "opened") {
		t.Fatalf("expected no match: body does not contain %q", body)
	}
}

func TestMatchesDraftAndBranchesRequirePullRequest(t *testing.T) {
	issue := baseItem()
	draft := true
	if Matches(protocol.GitHubMonitorFilters{Draft: &draft}, issue, "opened") {
		t.Fatalf("a Draft filter must not match a plain Issue")
	}

	pr := baseItem()
	pr.Kind = protocol.GitHubItemPullRequest
	pr.PullRequest = &protocol.GitHubPullRequestDetails{
		Draft: true,
		Base:  protocol.GitHubBranchRef{Ref: "main"},
		Head:  protocol.GitHubBranchRef{Ref: "feature/x"},
	}
	if !Matches(protocol.GitHubMonitorFilters{Draft: &draft}, pr, "opened") {
		t.Fatalf("expected draft match")
	}
	if !Matches(protocol.GitHubMonitorFilters{BaseBranches: []string{"main"}}, pr, "opened") {
		t.Fatalf("expected base branch match")
	}
	if Matches(protocol.GitHubMonitorFilters{HeadBranches: []string{"other"}}, pr, "opened") {
		t.Fatalf("expected no head branch match")
	}
}

func TestMatchesConflictingRequiresPullRequestAndMatchesValue(t *testing.T) {
	want := true
	issue := baseItem()
	if Matches(protocol.GitHubMonitorFilters{Conflicting: &want}, issue, "opened") {
		t.Fatalf("a Conflicting filter must not match a plain Issue")
	}

	conflicting := baseItem()
	conflicting.Kind = protocol.GitHubItemPullRequest
	conflicting.PullRequest = &protocol.GitHubPullRequestDetails{Conflicting: true}
	if !Matches(protocol.GitHubMonitorFilters{Conflicting: &want}, conflicting, "opened") {
		t.Fatalf("expected match: pull request has a conflict and filter wants Conflicting=true")
	}

	clean := baseItem()
	clean.Kind = protocol.GitHubItemPullRequest
	clean.PullRequest = &protocol.GitHubPullRequestDetails{Conflicting: false}
	if Matches(protocol.GitHubMonitorFilters{Conflicting: &want}, clean, "opened") {
		t.Fatalf("expected no match: pull request has no conflict but filter wants Conflicting=true")
	}
	wantFalse := false
	if !Matches(protocol.GitHubMonitorFilters{Conflicting: &wantFalse}, clean, "opened") {
		t.Fatalf("expected match: pull request has no conflict and filter wants Conflicting=false")
	}
}

func TestMatchesReviewersRequirePullRequestAndMatchRequestedReviewer(t *testing.T) {
	issue := baseItem()
	if Matches(protocol.GitHubMonitorFilters{Reviewers: []string{"alice"}}, issue, "opened") {
		t.Fatalf("a reviewer filter must not match a plain Issue")
	}

	pr := baseItem()
	pr.Kind = protocol.GitHubItemPullRequest
	pr.PullRequest = &protocol.GitHubPullRequestDetails{
		RequestedReviewers: []protocol.GitHubUser{{Login: "Octo-Reviewer"}},
	}
	if !Matches(protocol.GitHubMonitorFilters{Reviewers: []string{"octo-reviewer"}}, pr, "updated") {
		t.Fatalf("expected case-insensitive requested reviewer match")
	}
	if Matches(protocol.GitHubMonitorFilters{Reviewers: []string{"someone-else"}}, pr, "updated") {
		t.Fatalf("expected no match for a reviewer not requested on the pull request")
	}
}

func TestMatchesCreatedUpdatedTimeRange(t *testing.T) {
	item := baseItem()
	item.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item.UpdatedAt = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	after := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if !Matches(protocol.GitHubMonitorFilters{CreatedAfter: &after, CreatedBefore: &before}, item, "opened") {
		t.Fatalf("expected created time range match")
	}

	tooLate := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if Matches(protocol.GitHubMonitorFilters{CreatedAfter: &tooLate}, item, "opened") {
		t.Fatalf("expected no match: createdAt is before CreatedAfter bound")
	}
}

// The following tests cover ADR-007's central safety requirement: a
// Project condition must never match while Project data is unavailable,
// and must not be treated as a hard non-match either from the rule's
// perspective — it simply doesn't fire this cycle, and gets a fresh chance
// on the next poll once (or if) Project data becomes available.

func TestMatchesProjectConditionRequiresProjectData(t *testing.T) {
	condition := protocol.GitHubProjectFilterCondition{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}
	filters := protocol.GitHubMonitorFilters{Project: &condition}

	unavailable := baseItem()
	unavailable.ProjectsError = "insufficient permission"
	if Matches(filters, unavailable, "updated") {
		t.Fatalf("must not match while Projects data is unavailable, even though nothing rules it out")
	}

	neverFetched := baseItem() // ProjectFields is nil, ProjectsError is empty: never attempted
	if Matches(filters, neverFetched, "updated") {
		t.Fatalf("must not match when Projects has not been fetched at all")
	}
}

func TestMatchesProjectConditionValueMismatch(t *testing.T) {
	item := baseItem()
	item.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Todo"}}
	condition := protocol.GitHubProjectFilterCondition{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}
	if Matches(protocol.GitHubMonitorFilters{Project: &condition}, item, "updated") {
		t.Fatalf("must not match: Status is Todo, not Ready")
	}
}

func TestMatchesProjectConditionMatchesOnceDataArrives(t *testing.T) {
	item := baseItem()
	item.ProjectFields = []protocol.GitHubProjectFieldValue{{ProjectTitle: "Roadmap", FieldName: "Status", Value: "Ready"}}
	condition := protocol.GitHubProjectFilterCondition{ProjectTitle: "roadmap", FieldName: "status", Value: "ready"}
	if !Matches(protocol.GitHubMonitorFilters{Project: &condition}, item, "updated") {
		t.Fatalf("expected a case-insensitive match once Status = Ready")
	}
}

func TestRuleMatchesRespectsEnabledAndEventKinds(t *testing.T) {
	item := baseItem()
	rule := protocol.GitHubTriggerRule{
		Enabled:    true,
		EventKinds: []protocol.GitHubItemKind{protocol.GitHubItemPullRequest},
	}
	if RuleMatches(rule, item, "opened") {
		t.Fatalf("issue item must not match a rule scoped to pull_request only")
	}

	rule.EventKinds = []protocol.GitHubItemKind{protocol.GitHubItemIssue}
	if !RuleMatches(rule, item, "opened") {
		t.Fatalf("expected match once event kind includes issue")
	}

	rule.Enabled = false
	if RuleMatches(rule, item, "opened") {
		t.Fatalf("a disabled rule must never match")
	}
}
