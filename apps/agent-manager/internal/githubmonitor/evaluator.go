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
//   - When an item is new and the monitor's first-ever sync:
//     every new item establishes a baseline only, never fires a rule
//     (ADR-007 section 3), since there is nothing yet to consider a change
//     relative to. If an item was observed before (even in a partial first
//     sync that later failed), its state changes are evaluated normally.
//   - When nothing changed since the last poll (same normalized state
//     hash) and no rule was created or updated after that observation:
//     there is no new item or rule state to evaluate.
//
// A rule created or updated after the item's previous observation is
// evaluated against the current item even when the item itself is unchanged.
// A newly added rule can therefore fire for an existing Issue/PR. Updating a
// rule does not fire it again for an item for which that rule already produced
// an event, because delivery is unique per rule and item.
//
// The returned events are only the ones newly inserted this call: if
// any later matching observation for the same rule and item produces an
// identical delivery key and is silently dropped (ADR-007 section 6).
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
	evaluationAction := change.Action
	if !change.Changed && previousPtr != nil {
		evaluationAction = previousPtr.LastAction
		if evaluationAction == "" {
			evaluationAction = actionForCurrentState(item)
		}
	}
	firstSyncedAt := now
	evaluatedRuleVersions := map[string]string{}
	if previousPtr != nil {
		firstSyncedAt = previousPtr.FirstSyncedAt
		for ruleID, version := range previousPtr.EvaluatedRuleVersions {
			evaluatedRuleVersions[ruleID] = version
		}
	}
	observed := protocol.GitHubObservedItem{
		Repository:            monitor.Repository,
		Kind:                  item.Kind,
		Number:                item.Number,
		StateHash:             change.StateHash,
		LastAction:            evaluationAction,
		ProjectsAvailable:     item.HasProjectData(),
		Snapshot:              item,
		EvaluatedRuleVersions: evaluatedRuleVersions,
		FirstSyncedAt:         firstSyncedAt,
		ObservedAt:            now,
	}

	rules, err := e.store.ListTriggerRules(ctx, monitor.Repository)
	if err != nil {
		return nil, fmt.Errorf("evaluate github item: list trigger rules: %w", err)
	}

	isItemNewInFirstSync := monitor.LastSyncedAt == nil && previousPtr == nil
	if isItemNewInFirstSync {
		for _, rule := range rules {
			observed.EvaluatedRuleVersions[rule.ID] = ruleVersion(rule)
		}
		if _, err := e.store.ApplyItemObservation(ctx, observed, nil); err != nil {
			return nil, fmt.Errorf("evaluate github item: %w", err)
		}
		return nil, nil
	}

	candidates := make([]protocol.GitHubMonitorEvent, 0, len(rules))
	for _, rule := range rules {
		version := ruleVersion(rule)
		if !change.Changed && observed.EvaluatedRuleVersions[rule.ID] == version {
			continue
		}
		observed.EvaluatedRuleVersions[rule.ID] = version
		if !RuleMatches(rule, item, evaluationAction) {
			continue
		}
		id, err := e.newID()
		if err != nil {
			return nil, fmt.Errorf("evaluate github item: generate event id: %w", err)
		}
		ruleID := rule.ID
		key := DeliveryKey(monitor.Repository, item.Kind, item.Number, rule.ID)
		candidates = append(candidates, protocol.GitHubMonitorEvent{
			ID:              id,
			Repository:      monitor.Repository,
			RuleID:          &ruleID,
			Kind:            item.Kind,
			Number:          item.Number,
			Action:          evaluationAction,
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

func ruleVersion(rule protocol.GitHubTriggerRule) string {
	return rule.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

func actionForCurrentState(item protocol.GitHubItem) string {
	if item.State == protocol.GitHubItemClosed {
		return "closed"
	}
	return "opened"
}

func generateEventID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "github_event_" + hex.EncodeToString(random), nil
}
