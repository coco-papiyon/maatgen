package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

// InsertMonitorEvent records a detected/matched change as a new Outbox row
// (ADR-007 section 6). When event.DeliveryKey is set and an event with the
// same key already exists, the insert is silently skipped and inserted is
// false: the caller must treat this as "already delivered, do nothing,"
// not as an error. Events created by replaying an earlier event normally
// pass a nil DeliveryKey and are always inserted.
func (s *Store) InsertMonitorEvent(ctx context.Context, event protocol.GitHubMonitorEvent) (inserted bool, err error) {
	return insertMonitorEvent(ctx, s.db, event)
}

func insertMonitorEvent(ctx context.Context, exec execer, event protocol.GitHubMonitorEvent) (inserted bool, err error) {
	snapshot, err := json.Marshal(event.ItemSnapshot)
	if err != nil {
		return false, fmt.Errorf("insert github monitor event: encode item snapshot: %w", err)
	}
	_, err = exec.ExecContext(ctx, `INSERT INTO github_monitor_events(
		id, repository, rule_id, kind, number, action, before_state_hash, after_state_hash,
		delivery_key, status, skip_reason, replay_of_event_id, item_snapshot_json,
		session_id, run_id, last_error, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Repository, nullableString(event.RuleID), event.Kind, event.Number,
		event.Action, nullableString(event.BeforeStateHash), event.AfterStateHash,
		nullableString(event.DeliveryKey), event.Status, nullableString(event.SkipReason),
		nullableString(event.ReplayOfEventID), string(snapshot),
		nullableString(event.SessionID), nullableString(event.RunID), nullableString(event.LastError),
		formatTime(event.CreatedAt), formatTime(event.UpdatedAt))
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return false, nil
		}
		return false, fmt.Errorf("insert github monitor event: %w", err)
	}
	return true, nil
}

// CreateReplayEvent creates a new Outbox delivery that re-executes an
// earlier event without touching the original's dedupe key (ADR-007
// section 6): it copies the original's repository/rule/kind/number/action
// and item snapshot, sets status to queued (a replay is a deliberate,
// user-initiated execution, not a fresh detection that still needs
// evaluating), and leaves DeliveryKey nil.
func (s *Store) CreateReplayEvent(ctx context.Context, originalEventID, newEventID string, createdAt time.Time) (protocol.GitHubMonitorEvent, error) {
	original, err := s.GetMonitorEvent(ctx, originalEventID)
	if err != nil {
		return protocol.GitHubMonitorEvent{}, fmt.Errorf("create github monitor event replay: %w", err)
	}
	replay := protocol.GitHubMonitorEvent{
		ID:              newEventID,
		Repository:      original.Repository,
		RuleID:          original.RuleID,
		Kind:            original.Kind,
		Number:          original.Number,
		Action:          original.Action,
		AfterStateHash:  original.AfterStateHash,
		Status:          protocol.GitHubMonitorEventQueued,
		ReplayOfEventID: &originalEventID,
		ItemSnapshot:    original.ItemSnapshot,
		CreatedAt:       createdAt,
		UpdatedAt:       createdAt,
	}
	if _, err := s.InsertMonitorEvent(ctx, replay); err != nil {
		return protocol.GitHubMonitorEvent{}, fmt.Errorf("create github monitor event replay: %w", err)
	}
	return replay, nil
}

// UpdateMonitorEventStatus advances an event to a new status without
// otherwise changing it, e.g. detected -> matched, or matched -> queued.
func (s *Store) UpdateMonitorEventStatus(ctx context.Context, id string, status protocol.GitHubMonitorEventStatus, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE github_monitor_events SET status = ?, updated_at = ? WHERE id = ?`,
		status, formatTime(updatedAt), id)
	return updateResult("update github monitor event status", result, err)
}

// AttachMonitorEventSession records the Session created for this event and
// advances its status to session_created.
func (s *Store) AttachMonitorEventSession(ctx context.Context, id, sessionID string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE github_monitor_events
		SET session_id = ?, status = ?, updated_at = ? WHERE id = ?`,
		sessionID, protocol.GitHubMonitorEventSessionCreated, formatTime(updatedAt), id)
	return updateResult("attach github monitor event session", result, err)
}

// AttachMonitorEventRun records the Run started for this event's Session
// and advances its status to run_started.
func (s *Store) AttachMonitorEventRun(ctx context.Context, id, runID string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE github_monitor_events
		SET run_id = ?, status = ?, updated_at = ? WHERE id = ?`,
		runID, protocol.GitHubMonitorEventRunStarted, formatTime(updatedAt), id)
	return updateResult("attach github monitor event run", result, err)
}

// SkipMonitorEvent records why an event was not run (e.g. a "skip"
// concurrency policy while the repository's execution lock is held). The
// event is kept, not deleted, so it can still be replayed later.
func (s *Store) SkipMonitorEvent(ctx context.Context, id, reason string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE github_monitor_events
		SET status = ?, skip_reason = ?, updated_at = ?
		WHERE id = ? AND status IN (?, ?, ?)`,
		protocol.GitHubMonitorEventSkipped, reason, formatTime(updatedAt), id,
		protocol.GitHubMonitorEventDetected, protocol.GitHubMonitorEventMatched, protocol.GitHubMonitorEventQueued)
	return updateResult("skip github monitor event", result, err)
}

// FailMonitorEvent marks an event as failed with the given error.
func (s *Store) FailMonitorEvent(ctx context.Context, id, lastError string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE github_monitor_events
		SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		protocol.GitHubMonitorEventFailed, lastError, formatTime(updatedAt), id)
	return updateResult("fail github monitor event", result, err)
}

// GetMonitorEvent returns a single event by ID, or storage.ErrNotFound.
func (s *Store) GetMonitorEvent(ctx context.Context, id string) (protocol.GitHubMonitorEvent, error) {
	return scanMonitorEvent(s.db.QueryRowContext(ctx, monitorEventSelect+` WHERE id = ?`, id))
}

// ListMonitorEvents returns the most recent events for repository, newest
// first, capped at limit.
func (s *Store) ListMonitorEvents(ctx context.Context, repository string, limit int) ([]protocol.GitHubMonitorEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		monitorEventSelect+` WHERE repository = ? ORDER BY created_at DESC, id DESC LIMIT ?`, repository, limit)
	if err != nil {
		return nil, fmt.Errorf("list github monitor events: %w", err)
	}
	defer rows.Close()
	events := []protocol.GitHubMonitorEvent{}
	for rows.Next() {
		event, err := scanMonitorEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list github monitor events: %w", err)
	}
	return events, nil
}

// ListAllMonitorEvents returns the most recent events across every
// repository, newest first, capped at limit. Used by the Job screen's
// cross-repository event table, where events are shown independently of
// whichever repository is currently selected in the UI.
func (s *Store) ListAllMonitorEvents(ctx context.Context, limit int) ([]protocol.GitHubMonitorEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		monitorEventSelect+` ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list all github monitor events: %w", err)
	}
	defer rows.Close()
	events := []protocol.GitHubMonitorEvent{}
	for rows.Next() {
		event, err := scanMonitorEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list all github monitor events: %w", err)
	}
	return events, nil
}

// ListMonitorEventsByStatus returns events in status across every
// repository, oldest first. The Outbox dispatcher (ADR-007 section 6) uses
// this to find queued events to turn into Sessions/Runs, including after a
// restart.
func (s *Store) ListMonitorEventsByStatus(ctx context.Context, status protocol.GitHubMonitorEventStatus, limit int) ([]protocol.GitHubMonitorEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		monitorEventSelect+` WHERE status = ? ORDER BY created_at ASC, id ASC LIMIT ?`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("list github monitor events by status: %w", err)
	}
	defer rows.Close()
	events := []protocol.GitHubMonitorEvent{}
	for rows.Next() {
		event, err := scanMonitorEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list github monitor events by status: %w", err)
	}
	return events, nil
}

const monitorEventSelect = `SELECT id, repository, rule_id, kind, number, action, before_state_hash,
	after_state_hash, delivery_key, status, skip_reason, replay_of_event_id, item_snapshot_json,
	session_id, run_id, last_error, created_at, updated_at FROM github_monitor_events`

func scanMonitorEvent(row scanner) (protocol.GitHubMonitorEvent, error) {
	var event protocol.GitHubMonitorEvent
	var ruleID, beforeStateHash, deliveryKey, skipReason, replayOfEventID sql.NullString
	var itemSnapshot string
	var sessionID, runID, lastError sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(&event.ID, &event.Repository, &ruleID, &event.Kind, &event.Number, &event.Action,
		&beforeStateHash, &event.AfterStateHash, &deliveryKey, &event.Status, &skipReason, &replayOfEventID,
		&itemSnapshot, &sessionID, &runID, &lastError, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.GitHubMonitorEvent{}, storage.ErrNotFound
		}
		return protocol.GitHubMonitorEvent{}, fmt.Errorf("scan github monitor event: %w", err)
	}
	if ruleID.Valid {
		event.RuleID = &ruleID.String
	}
	if beforeStateHash.Valid {
		event.BeforeStateHash = &beforeStateHash.String
	}
	if deliveryKey.Valid {
		event.DeliveryKey = &deliveryKey.String
	}
	if skipReason.Valid {
		event.SkipReason = &skipReason.String
	}
	if replayOfEventID.Valid {
		event.ReplayOfEventID = &replayOfEventID.String
	}
	if err := json.Unmarshal([]byte(itemSnapshot), &event.ItemSnapshot); err != nil {
		return protocol.GitHubMonitorEvent{}, fmt.Errorf("scan github monitor event item snapshot: %w", err)
	}
	if sessionID.Valid {
		event.SessionID = &sessionID.String
	}
	if runID.Valid {
		event.RunID = &runID.String
	}
	if lastError.Valid {
		event.LastError = &lastError.String
	}
	var err error
	if event.CreatedAt, err = parseTime(createdAt); err != nil {
		return protocol.GitHubMonitorEvent{}, fmt.Errorf("scan github monitor event created_at: %w", err)
	}
	if event.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return protocol.GitHubMonitorEvent{}, fmt.Errorf("scan github monitor event updated_at: %w", err)
	}
	return event, nil
}
