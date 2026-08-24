package githubapi

import (
	"context"
	"fmt"
	"strconv"

	"github.com/shurcooL/githubv4"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// projectV2FieldValue mirrors one node of a ProjectV2Item's fieldValues
// connection. GitHub's GraphQL schema returns a union of concrete value
// types (text, single-select, number, date, ...); Typename records which
// one actually matched so resolve() knows which embedded fragment holds the
// value. Field name comes from the ProjectV2FieldCommon interface, which
// every field configuration type implements.
type projectV2FieldValue struct {
	Typename string `graphql:"__typename"`
	Text     struct {
		Text githubv4.String
	} `graphql:"... on ProjectV2ItemFieldTextValue"`
	SingleSelect struct {
		Name githubv4.String
	} `graphql:"... on ProjectV2ItemFieldSingleSelectValue"`
	Number struct {
		Number githubv4.Float
	} `graphql:"... on ProjectV2ItemFieldNumberValue"`
	Date struct {
		Date githubv4.Date
	} `graphql:"... on ProjectV2ItemFieldDateValue"`
	Field struct {
		Common struct {
			Name githubv4.String
		} `graphql:"... on ProjectV2FieldCommon"`
	} `graphql:"field"`
}

func (v projectV2FieldValue) resolve() (name, value string, ok bool) {
	name = string(v.Field.Common.Name)
	if name == "" {
		return "", "", false
	}
	switch v.Typename {
	case "ProjectV2ItemFieldTextValue":
		return name, string(v.Text.Text), true
	case "ProjectV2ItemFieldSingleSelectValue":
		return name, string(v.SingleSelect.Name), true
	case "ProjectV2ItemFieldNumberValue":
		return name, strconv.FormatFloat(float64(v.Number.Number), 'f', -1, 64), true
	case "ProjectV2ItemFieldDateValue":
		return name, v.Date.Date.Time.Format("2006-01-02"), true
	default:
		// Iteration fields and other value types not yet needed by any
		// filter (ADR-007 section 3) are ignored rather than guessed at.
		return "", "", false
	}
}

type projectV2Item struct {
	Project struct {
		Title githubv4.String
	}
	FieldValues struct {
		Nodes []projectV2FieldValue
	} `graphql:"fieldValues(first: 50)"`
}

type issueProjectItemsQuery struct {
	Repository struct {
		Issue struct {
			ProjectItems struct {
				Nodes []projectV2Item
			} `graphql:"projectItems(first: 20)"`
		} `graphql:"issue(number: $number)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

type pullRequestProjectItemsQuery struct {
	Repository struct {
		PullRequest struct {
			ProjectItems struct {
				Nodes []projectV2Item
			} `graphql:"projectItems(first: 20)"`
		} `graphql:"pullRequest(number: $number)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// FetchProjectFields retrieves the Project (V2) field values attached to a
// single Issue or Pull Request, across every Project the item belongs to.
// A returned error means Projects data is unavailable for this item
// (missing permission, Projects not used, a GraphQL error, ...); callers
// must record it as protocol.GitHubItem.ProjectsError rather than failing
// the surrounding Issue/PR fetch (ADR-007 section 2).
func (c *Client) FetchProjectFields(ctx context.Context, owner, repo string, kind protocol.GitHubItemKind, number int) ([]protocol.GitHubProjectFieldValue, error) {
	variables := map[string]any{
		"owner":  githubv4.String(owner),
		"name":   githubv4.String(repo),
		"number": githubv4.Int(number),
	}
	switch kind {
	case protocol.GitHubItemIssue:
		var query issueProjectItemsQuery
		if err := c.graphql.Query(ctx, &query, variables); err != nil {
			return nil, fmt.Errorf("query issue project items: %w", err)
		}
		return convertProjectItems(query.Repository.Issue.ProjectItems.Nodes), nil
	case protocol.GitHubItemPullRequest:
		var query pullRequestProjectItemsQuery
		if err := c.graphql.Query(ctx, &query, variables); err != nil {
			return nil, fmt.Errorf("query pull request project items: %w", err)
		}
		return convertProjectItems(query.Repository.PullRequest.ProjectItems.Nodes), nil
	default:
		return nil, fmt.Errorf("unsupported item kind %q", kind)
	}
}

func convertProjectItems(nodes []projectV2Item) []protocol.GitHubProjectFieldValue {
	fields := make([]protocol.GitHubProjectFieldValue, 0, len(nodes))
	for _, node := range nodes {
		projectTitle := string(node.Project.Title)
		for _, value := range node.FieldValues.Nodes {
			name, fieldValue, ok := value.resolve()
			if !ok {
				continue
			}
			fields = append(fields, protocol.GitHubProjectFieldValue{
				ProjectTitle: projectTitle,
				FieldName:    name,
				Value:        fieldValue,
			})
		}
	}
	return fields
}

// FetchItem retrieves the current state of a single Issue or Pull Request
// together with its Project field values. A Projects fetch failure is
// recorded on the returned item's ProjectsError instead of failing the
// call: ADR-007 section 2 requires that Project fetch failures never block
// Issue/PR monitoring. ProjectFields is a non-nil (possibly empty) slice on
// success, so protocol.GitHubItem.HasProjectData distinguishes "no Project
// entries" from "not fetched".
func (c *Client) FetchItem(ctx context.Context, owner, repo string, kind protocol.GitHubItemKind, number int) (protocol.GitHubItem, error) {
	var item protocol.GitHubItem
	var err error
	switch kind {
	case protocol.GitHubItemIssue:
		item, err = c.GetIssue(ctx, owner, repo, number)
	case protocol.GitHubItemPullRequest:
		item, err = c.GetPullRequest(ctx, owner, repo, number)
	default:
		return protocol.GitHubItem{}, fmt.Errorf("unsupported item kind %q", kind)
	}
	if err != nil {
		return protocol.GitHubItem{}, err
	}

	fields, projectsErr := c.FetchProjectFields(ctx, owner, repo, kind, number)
	if projectsErr != nil {
		item.ProjectsError = projectsErr.Error()
		return item, nil
	}
	item.ProjectFields = fields
	return item, nil
}
