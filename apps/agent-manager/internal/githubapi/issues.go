package githubapi

import (
	"context"
	"fmt"

	"github.com/google/go-github/v69/github"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// maxPages caps how many pages ListIssues/ListPullRequests will walk in a
// single call. It bounds worst-case API usage against very large
// repositories; it is a safety limit, not a page size callers should rely
// on for exhaustiveness.
const maxPages = 20

// ListOptions selects which Issues or Pull Requests to return.
type ListOptions struct {
	// State is "open", "closed", or "all". Empty means GitHub's default
	// ("open").
	State string
}

// ListIssues returns the repository's Issues, excluding Pull Requests.
// GitHub's Issues API returns both (every Pull Request is also an Issue);
// ADR-007 section 2 keeps them as separate normalized kinds, so Pull
// Requests are filtered out here and must be fetched via ListPullRequests.
func (c *Client) ListIssues(ctx context.Context, owner, repo string, opts ListOptions) ([]protocol.GitHubItem, error) {
	options := &github.IssueListByRepoOptions{
		State:       opts.State,
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var items []protocol.GitHubItem
	for page := 0; page < maxPages; page++ {
		issues, resp, err := c.rest.Issues.ListByRepo(ctx, owner, repo, options)
		if err != nil {
			return nil, fmt.Errorf("list issues: %w", err)
		}
		for _, issue := range issues {
			if issue.IsPullRequest() {
				continue
			}
			items = append(items, normalizeIssue(issue))
		}
		if resp.NextPage == 0 {
			break
		}
		options.Page = resp.NextPage
	}
	return items, nil
}

// GetIssue fetches a single Issue by number.
func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (protocol.GitHubItem, error) {
	issue, _, err := c.rest.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		return protocol.GitHubItem{}, fmt.Errorf("get issue: %w", err)
	}
	return normalizeIssue(issue), nil
}

// ListPullRequests returns the repository's Pull Requests.
func (c *Client) ListPullRequests(ctx context.Context, owner, repo string, opts ListOptions) ([]protocol.GitHubItem, error) {
	options := &github.PullRequestListOptions{
		State:       opts.State,
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var items []protocol.GitHubItem
	for page := 0; page < maxPages; page++ {
		pulls, resp, err := c.rest.PullRequests.List(ctx, owner, repo, options)
		if err != nil {
			return nil, fmt.Errorf("list pull requests: %w", err)
		}
		for _, pull := range pulls {
			items = append(items, normalizePullRequest(pull))
		}
		if resp.NextPage == 0 {
			break
		}
		options.Page = resp.NextPage
	}
	return items, nil
}

// GetPullRequest fetches a single Pull Request by number.
func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, number int) (protocol.GitHubItem, error) {
	pull, _, err := c.rest.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return protocol.GitHubItem{}, fmt.Errorf("get pull request: %w", err)
	}
	return normalizePullRequest(pull), nil
}

func normalizeIssue(issue *github.Issue) protocol.GitHubItem {
	item := protocol.GitHubItem{
		Kind:      protocol.GitHubItemIssue,
		Number:    issue.GetNumber(),
		Title:     issue.GetTitle(),
		Body:      issue.GetBody(),
		State:     protocol.GitHubItemState(issue.GetState()),
		Author:    protocol.GitHubUser{Login: issue.GetUser().GetLogin()},
		Assignees: []protocol.GitHubUser{},
		Labels:    []protocol.GitHubLabel{},
		CreatedAt: issue.GetCreatedAt().Time,
		UpdatedAt: issue.GetUpdatedAt().Time,
		URL:       issue.GetHTMLURL(),
	}
	for _, assignee := range issue.Assignees {
		item.Assignees = append(item.Assignees, protocol.GitHubUser{Login: assignee.GetLogin()})
	}
	for _, label := range issue.Labels {
		item.Labels = append(item.Labels, protocol.GitHubLabel{Name: label.GetName()})
	}
	if issue.Milestone != nil {
		item.Milestone = &protocol.GitHubMilestone{Title: issue.Milestone.GetTitle()}
	}
	return item
}

func normalizePullRequest(pull *github.PullRequest) protocol.GitHubItem {
	item := protocol.GitHubItem{
		Kind:      protocol.GitHubItemPullRequest,
		Number:    pull.GetNumber(),
		Title:     pull.GetTitle(),
		Body:      pull.GetBody(),
		State:     protocol.GitHubItemState(pull.GetState()),
		Author:    protocol.GitHubUser{Login: pull.GetUser().GetLogin()},
		Assignees: []protocol.GitHubUser{},
		Labels:    []protocol.GitHubLabel{},
		CreatedAt: pull.GetCreatedAt().Time,
		UpdatedAt: pull.GetUpdatedAt().Time,
		URL:       pull.GetHTMLURL(),
		PullRequest: &protocol.GitHubPullRequestDetails{
			Draft: pull.GetDraft(),
			Base:  protocol.GitHubBranchRef{Ref: pull.GetBase().GetRef(), SHA: pull.GetBase().GetSHA()},
			Head:  protocol.GitHubBranchRef{Ref: pull.GetHead().GetRef(), SHA: pull.GetHead().GetSHA()},
		},
	}
	for _, assignee := range pull.Assignees {
		item.Assignees = append(item.Assignees, protocol.GitHubUser{Login: assignee.GetLogin()})
	}
	for _, label := range pull.Labels {
		item.Labels = append(item.Labels, protocol.GitHubLabel{Name: label.GetName()})
	}
	if pull.Milestone != nil {
		item.Milestone = &protocol.GitHubMilestone{Title: pull.Milestone.GetTitle()}
	}
	return item
}
