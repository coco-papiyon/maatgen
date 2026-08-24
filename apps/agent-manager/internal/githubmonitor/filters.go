package githubmonitor

import (
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// RuleMatches reports whether rule should fire for item having just been
// observed with the given action (ADR-007 section 3). It checks that the
// rule is enabled, that its EventKinds include the item's kind, and that
// every populated field of its Filters matches.
func RuleMatches(rule protocol.GitHubTriggerRule, item protocol.GitHubItem, action string) bool {
	if !rule.Enabled {
		return false
	}
	if !containsKind(rule.EventKinds, item.Kind) {
		return false
	}
	return Matches(rule.Filters, item, action)
}

// Matches evaluates GitHubMonitorFilters against a detected change
// (ADR-007 section 3). Every populated field is ANDed; an empty/nil field
// imposes no constraint. List-valued fields (Actions, Authors, Assignees,
// Reviewers, Labels, ...) match if the item's corresponding value is any one of the
// listed values.
//
// A Project condition never matches while the item's Project data is
// unavailable (protocol.GitHubItem.HasProjectData is false): ADR-007
// section 2 requires that Project-gated rules not fire, and not be treated
// as definitively non-matching either, until Project data can actually be
// fetched — from the rule-matching side, both cases just mean "no match
// this cycle," and the poll will re-evaluate next time with fresh data.
func Matches(filters protocol.GitHubMonitorFilters, item protocol.GitHubItem, action string) bool {
	if len(filters.Actions) > 0 && !containsFold(filters.Actions, action) {
		return false
	}
	if len(filters.Numbers) > 0 && !containsInt(filters.Numbers, item.Number) {
		return false
	}
	if filters.TitleContains != nil && !containsFoldSubstring(item.Title, *filters.TitleContains) {
		return false
	}
	if filters.BodyContains != nil && !containsFoldSubstring(item.Body, *filters.BodyContains) {
		return false
	}
	if len(filters.Authors) > 0 && !containsFold(filters.Authors, item.Author.Login) {
		return false
	}
	if len(filters.Assignees) > 0 && !anyFold(filters.Assignees, userLogins(item.Assignees)) {
		return false
	}
	if len(filters.Reviewers) > 0 && !matchesReviewers(filters.Reviewers, item.PullRequest) {
		return false
	}
	if len(filters.Labels) > 0 && !anyFold(filters.Labels, labelNames(item.Labels)) {
		return false
	}
	if len(filters.Milestones) > 0 && !matchesMilestone(filters.Milestones, item.Milestone) {
		return false
	}
	states := filters.States
	if len(states) == 0 {
		states = []protocol.GitHubItemState{protocol.GitHubItemOpen}
	}
	if !containsState(states, item.State) {
		return false
	}
	if filters.Draft != nil && !matchesDraft(*filters.Draft, item.PullRequest) {
		return false
	}
	if len(filters.BaseBranches) > 0 && !matchesBranch(filters.BaseBranches, item.PullRequest, true) {
		return false
	}
	if len(filters.HeadBranches) > 0 && !matchesBranch(filters.HeadBranches, item.PullRequest, false) {
		return false
	}
	if filters.Project != nil && !matchesProject(*filters.Project, item) {
		return false
	}
	if filters.CreatedAfter != nil && !item.CreatedAt.After(*filters.CreatedAfter) {
		return false
	}
	if filters.CreatedBefore != nil && !item.CreatedAt.Before(*filters.CreatedBefore) {
		return false
	}
	if filters.UpdatedAfter != nil && !item.UpdatedAt.After(*filters.UpdatedAfter) {
		return false
	}
	if filters.UpdatedBefore != nil && !item.UpdatedAt.Before(*filters.UpdatedBefore) {
		return false
	}
	return true
}

func containsKind(kinds []protocol.GitHubItemKind, kind protocol.GitHubItemKind) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func containsState(states []protocol.GitHubItemState, state protocol.GitHubItemState) bool {
	for _, s := range states {
		if s == state {
			return true
		}
	}
	return false
}

func containsInt(values []int, target int) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func containsFold(values []string, target string) bool {
	for _, v := range values {
		if strings.EqualFold(v, target) {
			return true
		}
	}
	return false
}

func containsFoldSubstring(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// anyFold reports whether any of candidates case-insensitively equals any
// of values.
func anyFold(values, candidates []string) bool {
	for _, candidate := range candidates {
		if containsFold(values, candidate) {
			return true
		}
	}
	return false
}

func matchesMilestone(titles []string, milestone *protocol.GitHubMilestone) bool {
	if milestone == nil {
		return false
	}
	return containsFold(titles, milestone.Title)
}

func matchesDraft(want bool, pr *protocol.GitHubPullRequestDetails) bool {
	if pr == nil {
		return false
	}
	return pr.Draft == want
}

func matchesReviewers(reviewers []string, pr *protocol.GitHubPullRequestDetails) bool {
	if pr == nil {
		return false
	}
	return anyFold(reviewers, userLogins(pr.RequestedReviewers))
}

func matchesBranch(branches []string, pr *protocol.GitHubPullRequestDetails, base bool) bool {
	if pr == nil {
		return false
	}
	ref := pr.Head.Ref
	if base {
		ref = pr.Base.Ref
	}
	return containsFold(branches, ref)
}

func matchesProject(condition protocol.GitHubProjectFilterCondition, item protocol.GitHubItem) bool {
	if !item.HasProjectData() {
		return false
	}
	value, ok := item.ProjectFieldValue(condition.ProjectTitle, condition.FieldName)
	if !ok {
		return false
	}
	return strings.EqualFold(value, condition.Value)
}
