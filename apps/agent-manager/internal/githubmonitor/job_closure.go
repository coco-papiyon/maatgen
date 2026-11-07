package githubmonitor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
)

// closeJobsForClosedItems reconciles existing Jobs without scanning closed
// GitHub history or evaluating closed items against trigger rules.
func (p *Poller) closeJobsForClosedItems(ctx context.Context, client GitHubClient, monitor protocol.GitHubRepositoryMonitor, issues, pulls []protocol.GitHubItem) error {
	if p.closeEvent == nil {
		return nil
	}
	// SQLite LIMIT -1 means unlimited: older Jobs must not be starved by
	// the UI's recent-event limit or by newer Jobs that remain open.
	events, err := p.store.ListMonitorEvents(ctx, monitor.Repository, -1, "open")
	if err != nil {
		return fmt.Errorf("sync github job closure: list jobs: %w", err)
	}
	type key struct {
		kind   protocol.GitHubItemKind
		number int
	}
	states := map[key]protocol.GitHubItemState{}
	for _, items := range [][]protocol.GitHubItem{issues, pulls} {
		for _, item := range items {
			states[key{item.Kind, item.Number}] = protocol.GitHubItemOpen
		}
	}
	var failures []error
	for _, event := range events {
		path := "issues"
		if event.Kind == protocol.GitHubItemPullRequest {
			path = "pull"
		} else if event.Kind != protocol.GitHubItemIssue {
			continue
		}
		// The local repository's remote may have changed since this Job was
		// created. Never interpret the same number in a different repository.
		expectedURL := fmt.Sprintf("https://%s/%s/%s/%s/%d", monitor.Host, monitor.Owner, monitor.Name, path, event.Number)
		if !strings.EqualFold(event.ItemSnapshot.URL, expectedURL) {
			continue
		}
		itemKey := key{event.Kind, event.Number}
		state, checked := states[itemKey]
		if !checked {
			var item protocol.GitHubItem
			if event.Kind == protocol.GitHubItemPullRequest {
				item, err = client.GetPullRequest(ctx, monitor.Owner, monitor.Name, event.Number)
			} else {
				item, err = client.GetIssue(ctx, monitor.Owner, monitor.Name, event.Number)
			}
			// Cache failures as unknown for this cycle, but retry next sync.
			states[itemKey] = ""
			if err != nil {
				failures = append(failures, fmt.Errorf("fetch %s #%d for job closure: %w", event.Kind, event.Number, err))
				continue
			}
			state = item.State
			states[itemKey] = state
		}
		if state != protocol.GitHubItemClosed {
			continue
		}
		if _, err := p.closeEvent(ctx, event.ID); err != nil && !errors.Is(err, session.ErrRunActive) {
			failures = append(failures, fmt.Errorf("close github job %s: %w", event.ID, err))
		}
	}
	return errors.Join(failures...)
}
