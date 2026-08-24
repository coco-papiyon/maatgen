package githubmonitor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

// Store is the persistence dependency Evaluator needs. It is satisfied by
// *sqlite.Store.
type Store interface {
	GetObservedItem(ctx context.Context, repository string, kind protocol.GitHubItemKind, number int) (protocol.GitHubObservedItem, error)
	ListTriggerRules(ctx context.Context, repository string) ([]protocol.GitHubTriggerRule, error)
	ApplyItemObservation(ctx context.Context, item protocol.GitHubObservedItem, events []protocol.GitHubMonitorEvent) (inserted []bool, err error)
}

// Evaluator turns freshly fetched GitHub items into Outbox events by
// detecting what changed and matching it against a repository's Trigger
// Rules (ADR-007 sections 3 and 6).
type Evaluator struct {
	store Store
	now   func() time.Time
	newID func() (string, error)
}

func NewEvaluator(store Store) *Evaluator {
	return &Evaluator{store: store, now: time.Now, newID: generateEventID}
}

// EvaluateItem processes one item fetched from GitHub during a poll of
// monitor. It always records the item's new observed baseline. It creates
// a queued Outbox event (ADR-007 section 6) for every enabled rule that
// matches the detected change, except:
//
//   - During the monitor's first-ever sync (monitor.LastSyncedAt == nil):
//     every fetched item establishes a baseline only, never fires a rule
//     (ADR-007 section 3), since there is nothing yet to consider a change
//     relative to.
//   - When nothing changed since the last poll (same normalized state
//     hash): there is no new change to evaluate rules against.
//
// The returned events are only the ones newly inserted this call: if
// re-evaluating the exact same detected change against the exact same rule
// (e.g. a repeated poll before the baseline advances) would produce an
// identical delivery key to an event already recorded, that duplicate is
// silently dropped and omitted from the result (ADR-007 section 6).
func (e *Evaluator) EvaluateItem(ctx context.Context, monitor protocol.GitHubRepositoryMonitor, item protocol.GitHubItem) ([]protocol.GitHubMonitorEvent, error) {
	previous, err := e.store.GetObservedItem(ctx, monitor.Repository, item.Kind, item.Number)
	var previousPtr *protocol.GitHubObservedItem
	switch {
	case err == nil:
		previousPtr = &previous
	case errors.Is(err, storage.ErrNotFound):
		previousPtr = nil
	default:
		return nil, fmt.Errorf("evaluate github item: %w", err)
	}

	change := DetectChange(previousPtr, item)
	now := e.now().UTC()
	firstSyncedAt := now
	if previousPtr != nil {
		firstSyncedAt = previousPtr.FirstSyncedAt
	}
	observed := protocol.GitHubObservedItem{
		Repository:        monitor.Repository,
		Kind:              item.Kind,
		Number:            item.Number,
		StateHash:         change.StateHash,
		LastAction:        change.Action,
		ProjectsAvailable: item.HasProjectData(),
		Snapshot:          item,
		FirstSyncedAt:     firstSyncedAt,
		ObservedAt:        now,
	}

	isMonitorFirstSync := monitor.LastSyncedAt == nil
	if isMonitorFirstSync || !change.Changed {
		if _, err := e.store.ApplyItemObservation(ctx, observed, nil); err != nil {
			return nil, fmt.Errorf("evaluate github item: %w", err)
		}
		return nil, nil
	}

	rules, err := e.store.ListTriggerRules(ctx, monitor.Repository)
	if err != nil {
		return nil, fmt.Errorf("evaluate github item: list trigger rules: %w", err)
	}

	candidates := make([]protocol.GitHubMonitorEvent, 0, len(rules))
	for _, rule := range rules {
		if !RuleMatches(rule, item, change.Action) {
			continue
		}
		id, err := e.newID()
		if err != nil {
			return nil, fmt.Errorf("evaluate github item: generate event id: %w", err)
		}
		ruleID := rule.ID
		key := DeliveryKey(monitor.Repository, item.Kind, item.Number, change.Action, item.UpdatedAt, change.StateHash, rule.ID)
		candidates = append(candidates, protocol.GitHubMonitorEvent{
			ID:              id,
			Repository:      monitor.Repository,
			RuleID:          &ruleID,
			Kind:            item.Kind,
			Number:          item.Number,
			Action:          change.Action,
			BeforeStateHash: change.BeforeStateHash,
			AfterStateHash:  change.StateHash,
			DeliveryKey:     &key,
			Status:          protocol.GitHubMonitorEventQueued,
			ItemSnapshot:    item,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}

	inserted, err := e.store.ApplyItemObservation(ctx, observed, candidates)
	if err != nil {
		return nil, fmt.Errorf("evaluate github item: %w", err)
	}

	result := make([]protocol.GitHubMonitorEvent, 0, len(candidates))
	for i, ok := range inserted {
		if ok {
			result = append(result, candidates[i])
		}
	}
	return result, nil
}

func generateEventID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "github_event_" + hex.EncodeToString(random), nil
}
