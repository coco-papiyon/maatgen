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
		session_id, run_id, last_error, created_at, updated_at, closed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.Repository, nullableString(event.RuleID), event.Kind, event.Number,
		event.Action, nullableString(event.BeforeStateHash), event.AfterStateHash,
		nullableString(event.DeliveryKey), event.Status, nullableString(event.SkipReason),
		nullableString(event.ReplayOfEventID), string(snapshot),
		nullableString(event.SessionID), nullableString(event.RunID), nullableString(event.LastError),
		formatTime(event.CreatedAt), formatTime(event.UpdatedAt), nullableTime(event.ClosedAt))
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

// CloseMonitorEvent closes a single event by ID (the Job screen's close
// action, Issue #34): its Status is overwritten to closed, the same way
// CloseSession collapses AgentSession.Status regardless of prior activity.
// It is idempotent: closing an already-closed event is a no-op, and
// ClosedAt is only ever set once (COALESCE), matching CloseSession's
// closed_at semantics.
func (s *Store) CloseMonitorEvent(ctx context.Context, id string, closedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE github_monitor_events
		SET status = ?, closed_at = COALESCE(closed_at, ?), updated_at = ?
		WHERE id = ? AND status != ?`,
		protocol.GitHubMonitorEventClosed, formatTime(closedAt), formatTime(closedAt), id, protocol.GitHubMonitorEventClosed)
	if err != nil {
		return fmt.Errorf("close github monitor event: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM github_monitor_events WHERE id = ?", id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return storage.ErrNotFound
		}
		// Already closed: idempotent no-op.
	}
	return nil
}

// CloseMonitorEventForSession closes whichever event created sessionID, if
// any (the Session→Job cascade side of Issue #34: closing a Session must
// also close the Job that started it). It is a silent no-op when no event
// references this Session (e.g. a manually created session with no GitHub
// trigger), and idempotent like CloseMonitorEvent.
func (s *Store) CloseMonitorEventForSession(ctx context.Context, sessionID string, closedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE github_monitor_events
		SET status = ?, closed_at = COALESCE(closed_at, ?), updated_at = ?
		WHERE session_id = ? AND status != ?`,
		protocol.GitHubMonitorEventClosed, formatTime(closedAt), formatTime(closedAt), sessionID, protocol.GitHubMonitorEventClosed)
	if err != nil {
		return fmt.Errorf("close github monitor event for session: %w", err)
	}
	return nil
}

// GetMonitorEvent returns a single event by ID, or storage.ErrNotFound.
func (s *Store) GetMonitorEvent(ctx context.Context, id string) (protocol.GitHubMonitorEvent, error) {
	return scanMonitorEvent(s.db.QueryRowContext(ctx, monitorEventSelect+` WHERE id = ?`, id))
}

// ListMonitorEvents returns the most recent events for repository, newest
// first, capped at limit, narrowed by filter (see monitorEventStatusFilter).
func (s *Store) ListMonitorEvents(ctx context.Context, repository string, limit int, filter string) ([]protocol.GitHubMonitorEvent, error) {
	condition, filterArgs := monitorEventStatusFilter(filter)
	query := monitorEventSelect + ` WHERE repository = ?`
	args := append([]any{repository}, filterArgs...)
	if condition != "" {
		query += ` AND ` + condition
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
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
// repository, newest first, capped at limit, narrowed by filter (see
// monitorEventStatusFilter). Used by the Job screen's cross-repository
// event table, where events are shown independently of whichever
// repository is currently selected in the UI.
func (s *Store) ListAllMonitorEvents(ctx context.Context, limit int, filter string) ([]protocol.GitHubMonitorEvent, error) {
	condition, filterArgs := monitorEventStatusFilter(filter)
	query := monitorEventSelect
	args := append([]any{}, filterArgs...)
	if condition != "" {
		query += ` WHERE ` + condition
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
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

// monitorEventStatusFilter translates the Job list's status filter (Issue
// #34) into a SQL condition (without a leading WHERE/AND) and its bind
// args. "" and "open" exclude closed events regardless of their other
// status; "all" applies no filter; any other value (a
// protocol.GitHubMonitorEventStatus, including "closed" itself) is an
// exact match. Callers must validate filter against
// protocol.AllGitHubMonitorEventStatuses plus "open"/"all" before calling
// this (see server.parseJobStatusFilter) so an unrecognized value can't
// silently produce an empty result set.
func monitorEventStatusFilter(filter string) (condition string, args []any) {
	switch filter {
	case "", "open":
		return "status != ?", []any{protocol.GitHubMonitorEventClosed}
	case "all":
		return "", nil
	default:
		return "status = ?", []any{filter}
	}
}

const monitorEventSelect = `SELECT id, repository, rule_id, kind, number, action, before_state_hash,
	after_state_hash, delivery_key, status, skip_reason, replay_of_event_id, item_snapshot_json,
	session_id, run_id, last_error, created_at, updated_at, closed_at FROM github_monitor_events`

func scanMonitorEvent(row scanner) (protocol.GitHubMonitorEvent, error) {
	var event protocol.GitHubMonitorEvent
	var ruleID, beforeStateHash, deliveryKey, skipReason, replayOfEventID sql.NullString
	var itemSnapshot string
	var sessionID, runID, lastError sql.NullString
	var createdAt, updatedAt string
	var closedAt sql.NullString
	if err := row.Scan(&event.ID, &event.Repository, &ruleID, &event.Kind, &event.Number, &event.Action,
		&beforeStateHash, &event.AfterStateHash, &deliveryKey, &event.Status, &skipReason, &replayOfEventID,
		&itemSnapshot, &sessionID, &runID, &lastError, &createdAt, &updatedAt, &closedAt); err != nil {
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
	if closedAt.Valid {
		parsed, err := parseTime(closedAt.String)
		if err != nil {
			return protocol.GitHubMonitorEvent{}, fmt.Errorf("scan github monitor event closed_at: %w", err)
		}
		event.ClosedAt = &parsed
	}
	return event, nil
}
