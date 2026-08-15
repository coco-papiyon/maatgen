package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func (s *Store) AppendEvent(ctx context.Context, event protocol.SessionEvent) (protocol.SessionEvent, error) {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = protocol.SchemaVersion
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if len(event.Data) == 0 {
		event.Data = json.RawMessage(`{}`)
	}
	if !json.Valid(event.Data) {
		return protocol.SessionEvent{}, fmt.Errorf("append event: invalid data JSON")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.SessionEvent{}, fmt.Errorf("append event: begin: %w", err)
	}
	defer tx.Rollback()

	if event.Sequence == 0 {
		if err := tx.QueryRowContext(ctx,
			"SELECT COALESCE(MAX(sequence), 0) + 1 FROM events WHERE session_id = ?",
			event.SessionID,
		).Scan(&event.Sequence); err != nil {
			return protocol.SessionEvent{}, fmt.Errorf("append event: allocate sequence: %w", err)
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO events(
			id, session_id, run_id, sequence, source, event_type,
			schema_version, payload_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.SessionID,
		nullableString(event.RunID),
		event.Sequence,
		event.Source,
		event.Type,
		event.SchemaVersion,
		string(event.Data),
		formatTime(event.Timestamp),
	)
	if err != nil {
		return protocol.SessionEvent{}, fmt.Errorf("append event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return protocol.SessionEvent{}, fmt.Errorf("append event: commit: %w", err)
	}
	if s.eventPublisher != nil {
		s.eventPublisher.Publish(event.SessionID)
	}
	return event, nil
}

func (s *Store) ListEventsAfter(
	ctx context.Context,
	sessionID string,
	afterSequence int64,
	limit int,
) ([]protocol.SessionEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, run_id, sequence, source, event_type,
		       schema_version, payload_json, created_at
		FROM events
		WHERE session_id = ? AND sequence > ?
		ORDER BY sequence ASC
		LIMIT ?`, sessionID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []protocol.SessionEvent
	for rows.Next() {
		var event protocol.SessionEvent
		var runID sql.NullString
		var payload, createdAt string
		if err := rows.Scan(
			&event.ID,
			&event.SessionID,
			&runID,
			&event.Sequence,
			&event.Source,
			&event.Type,
			&event.SchemaVersion,
			&payload,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("list events: scan: %w", err)
		}
		if runID.Valid {
			event.RunID = &runID.String
		}
		event.Data = json.RawMessage(payload)
		event.Timestamp, err = parseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("list events: parse time: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return events, nil
}

func (s *Store) UpsertRunUsage(ctx context.Context, runID string, usage protocol.TokenUsage, rawJSON json.RawMessage) error {
	if len(rawJSON) > 0 && !json.Valid(rawJSON) {
		return fmt.Errorf("upsert run usage: invalid raw JSON")
	}
	var raw any
	if len(rawJSON) > 0 {
		raw = string(rawJSON)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO run_usage(
			run_id, input_tokens, cached_input_tokens, output_tokens,
			reasoning_output_tokens, total_tokens, source, raw_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			input_tokens = excluded.input_tokens,
			cached_input_tokens = excluded.cached_input_tokens,
			output_tokens = excluded.output_tokens,
			reasoning_output_tokens = excluded.reasoning_output_tokens,
			total_tokens = excluded.total_tokens,
			source = excluded.source,
			raw_json = excluded.raw_json`,
		runID,
		nullableInt64(usage.InputTokens),
		nullableInt64(usage.CachedInputTokens),
		nullableInt64(usage.OutputTokens),
		nullableInt64(usage.ReasoningOutputTokens),
		nullableInt64(usage.TotalTokens),
		usage.Source,
		raw,
	)
	if err != nil {
		return fmt.Errorf("upsert run usage: %w", err)
	}
	return nil
}

func (s *Store) GetRunUsage(ctx context.Context, runID string) (protocol.TokenUsage, json.RawMessage, error) {
	var usage protocol.TokenUsage
	var input, cached, output, reasoning, total sql.NullInt64
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT input_tokens, cached_input_tokens, output_tokens,
		       reasoning_output_tokens, total_tokens, source, raw_json
		FROM run_usage WHERE run_id = ?`, runID).Scan(
		&input,
		&cached,
		&output,
		&reasoning,
		&total,
		&usage.Source,
		&raw,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return protocol.TokenUsage{}, nil, storage.ErrNotFound
		}
		return protocol.TokenUsage{}, nil, fmt.Errorf("get run usage: %w", err)
	}
	usage.InputTokens = int64Pointer(input)
	usage.CachedInputTokens = int64Pointer(cached)
	usage.OutputTokens = int64Pointer(output)
	usage.ReasoningOutputTokens = int64Pointer(reasoning)
	usage.TotalTokens = int64Pointer(total)
	if raw.Valid {
		return usage, json.RawMessage(raw.String), nil
	}
	return usage, nil, nil
}

func (s *Store) AppendRedactedRawEvent(ctx context.Context, event storage.RedactedRawEvent) (storage.RedactedRawEvent, error) {
	if !json.Valid(event.RawJSON) {
		return storage.RedactedRawEvent{}, fmt.Errorf("append redacted raw event: invalid JSON")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO redacted_raw_events(
			session_id, run_id, agent, raw_json, created_at
		) VALUES (?, ?, ?, ?, ?)`,
		event.SessionID,
		nullableString(event.RunID),
		event.Agent,
		string(event.RawJSON),
		formatTime(event.CreatedAt),
	)
	if err != nil {
		return storage.RedactedRawEvent{}, fmt.Errorf("append redacted raw event: %w", err)
	}
	event.ID, err = result.LastInsertId()
	if err != nil {
		return storage.RedactedRawEvent{}, fmt.Errorf("append redacted raw event: id: %w", err)
	}
	return event, nil
}

func (s *Store) ListRedactedRawEvents(ctx context.Context, runID string, limit int) ([]storage.RedactedRawEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, run_id, agent, raw_json, created_at
		FROM redacted_raw_events
		WHERE run_id = ?
		ORDER BY id ASC
		LIMIT ?`, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("list redacted raw events: %w", err)
	}
	defer rows.Close()

	var events []storage.RedactedRawEvent
	for rows.Next() {
		var event storage.RedactedRawEvent
		var storedRunID sql.NullString
		var raw, createdAt string
		if err := rows.Scan(
			&event.ID,
			&event.SessionID,
			&storedRunID,
			&event.Agent,
			&raw,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("list redacted raw events: scan: %w", err)
		}
		if storedRunID.Valid {
			event.RunID = &storedRunID.String
		}
		event.RawJSON = json.RawMessage(raw)
		event.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, fmt.Errorf("list redacted raw events: parse time: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list redacted raw events: %w", err)
	}
	return events, nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func int64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}
