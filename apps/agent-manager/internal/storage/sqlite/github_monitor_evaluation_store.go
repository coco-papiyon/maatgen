package sqlite

import (
	"context"
	"fmt"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// ApplyItemObservation atomically records the outcome of evaluating one
// freshly fetched GitHub item: it upserts the item's new observed baseline
// and inserts every Outbox event produced by matching trigger rules against
// the detected change, all in a single transaction (ADR-007 section 6).
//
// Atomicity matters here specifically because event insertion is
// dedupe-key-guarded against the *previous* observed state: if the baseline
// were committed separately (before or after), a crash in between could
// either lose an already-matched event forever (next poll would see no
// diff and never retry) or leave a delivery key pointing at an event that
// never got the chance to progress. Doing both in one transaction rules
// that out.
//
// inserted[i] reports whether events[i] was newly inserted; false means an
// event with the same DeliveryKey already existed and this one was
// silently skipped, per InsertMonitorEvent's dedupe contract. events may be
// empty (nothing matched, or this was the monitor's first sync).
func (s *Store) ApplyItemObservation(ctx context.Context, item protocol.GitHubObservedItem, events []protocol.GitHubMonitorEvent) (inserted []bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("apply github item observation: begin transaction: %w", err)
	}

	inserted = make([]bool, len(events))
	for i, event := range events {
		ok, err := insertMonitorEvent(ctx, tx, event)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("apply github item observation: insert event: %w", err)
		}
		inserted[i] = ok
	}

	if err := upsertObservedItem(ctx, tx, item); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("apply github item observation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("apply github item observation: commit: %w", err)
	}
	return inserted, nil
}
