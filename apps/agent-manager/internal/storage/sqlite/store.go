package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	db             *sql.DB
	eventPublisher EventPublisher
}

type EventPublisher interface {
	Publish(sessionID string)
}

type Option func(*Store)

func WithEventPublisher(publisher EventPublisher) Option {
	return func(store *Store) {
		store.eventPublisher = publisher
	}
}

func Open(ctx context.Context, path string, options ...Option) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	for _, option := range options {
		option(store)
	}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initialize(ctx context.Context) error {
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
	} {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}
	return s.migrate(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return fmt.Errorf("invalid migration name %q", entry.Name())
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			return fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}

		var applied int
		err = s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'",
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration table: %w", err)
		}
		if applied > 0 {
			err = s.db.QueryRowContext(ctx,
				"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version,
			).Scan(&applied)
			if err != nil {
				return fmt.Errorf("check migration %d: %w", version, err)
			}
			if applied > 0 {
				continue
			}
		}

		content, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
			version, formatTime(time.Now().UTC()),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, session protocol.AgentSession) error {
	if session.CleanupStatus == "" {
		session.CleanupStatus = protocol.CleanupNotStarted
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(
			id, agent, workspace, worktree, base_commit, codex_thread_id,
			status, created_at, closed_at, cleanup_status, cleanup_error,
			cleanup_attempts, cleanup_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.Agent,
		session.Workspace,
		session.Worktree,
		session.BaseCommit,
		nullableString(session.CodexThreadID),
		session.Status,
		formatTime(session.CreatedAt),
		nullableTime(session.ClosedAt),
		session.CleanupStatus,
		nullableString(session.CleanupError),
		session.CleanupAttempts,
		nullableTime(session.CleanupUpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, agent, workspace, worktree, base_commit, codex_thread_id,
		       status, created_at, closed_at, cleanup_status, cleanup_error,
		       cleanup_attempts, cleanup_updated_at
		FROM sessions WHERE id = ?`, id)
	return scanSession(row)
}

func (s *Store) ListSessions(ctx context.Context, limit int, before *protocol.SessionCursor) ([]protocol.AgentSession, error) {
	if limit <= 0 || limit > 501 {
		limit = 100
	}
	query := `
		SELECT id, agent, workspace, worktree, base_commit, codex_thread_id,
		       status, created_at, closed_at, cleanup_status, cleanup_error,
		       cleanup_attempts, cleanup_updated_at
		FROM sessions`
	args := []any{}
	if before != nil {
		query += ` WHERE julianday(created_at) < julianday(?)
			OR (julianday(created_at) = julianday(?) AND id < ?)`
		formatted := formatTime(before.CreatedAt)
		args = append(args, formatted, formatted, before.ID)
	}
	query += ` ORDER BY julianday(created_at) DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []protocol.AgentSession
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return sessions, nil
}

func (s *Store) UpdateSessionThreadID(ctx context.Context, id, threadID string) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET codex_thread_id = ? WHERE id = ?", threadID, id,
	)
	return updateResult("update session thread", result, err)
}

func (s *Store) CloseSession(ctx context.Context, id string, closedAt time.Time) error {
	result, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET status = ?, closed_at = ? WHERE id = ?",
		protocol.SessionClosed, formatTime(closedAt), id,
	)
	return updateResult("close session", result, err)
}

func (s *Store) PrepareSessionCleanup(ctx context.Context, id string, closedAt time.Time) (protocol.AgentSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return protocol.AgentSession{}, fmt.Errorf("begin session cleanup: %w", err)
	}
	defer tx.Rollback()

	query := `
		SELECT id, agent, workspace, worktree, base_commit, codex_thread_id,
		       status, created_at, closed_at, cleanup_status, cleanup_error,
		       cleanup_attempts, cleanup_updated_at
		FROM sessions WHERE id = ?`
	session, err := scanSession(tx.QueryRowContext(ctx, query, id))
	if err != nil {
		return protocol.AgentSession{}, err
	}
	if session.CleanupStatus == protocol.CleanupCompleted {
		return session, nil
	}
	var activeRuns int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM runs
		WHERE session_id = ? AND status IN ('queued', 'starting', 'running')`, id,
	).Scan(&activeRuns); err != nil {
		return protocol.AgentSession{}, fmt.Errorf("check active runs: %w", err)
	}
	if activeRuns > 0 {
		return protocol.AgentSession{}, storage.ErrRunActive
	}
	updatedAt := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET status = ?, closed_at = COALESCE(closed_at, ?), cleanup_status = ?,
		    cleanup_error = NULL, cleanup_attempts = cleanup_attempts + 1,
		    cleanup_updated_at = ?
		WHERE id = ?`,
		protocol.SessionClosed, formatTime(closedAt), protocol.CleanupPending,
		formatTime(updatedAt), id,
	); err != nil {
		return protocol.AgentSession{}, fmt.Errorf("prepare session cleanup: %w", err)
	}
	session, err = scanSession(tx.QueryRowContext(ctx, query, id))
	if err != nil {
		return protocol.AgentSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.AgentSession{}, fmt.Errorf("commit session cleanup: %w", err)
	}
	return session, nil
}

func (s *Store) FinishSessionCleanup(ctx context.Context, id string, status protocol.CleanupStatus, cleanupError *string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET cleanup_status = ?, cleanup_error = ?, cleanup_updated_at = ?
		WHERE id = ?`,
		status, nullableString(cleanupError), formatTime(updatedAt), id,
	)
	return updateResult("finish session cleanup", result, err)
}

func (s *Store) CreateRun(ctx context.Context, run protocol.AgentRun) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO runs(
			id, session_id, status, prompt, started_at, finished_at, exit_code, created_at
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?
		FROM sessions
		WHERE id = ? AND status = ?`,
		run.ID,
		run.SessionID,
		run.Status,
		run.Prompt,
		nullableTime(run.StartedAt),
		nullableTime(run.FinishedAt),
		nullableInt(run.ExitCode),
		formatTime(time.Now().UTC()),
		run.SessionID,
		protocol.SessionActive,
	)
	if err != nil {
		return fmt.Errorf("create run: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("create run rows affected: %w", err)
	}
	if affected == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", run.SessionID).Scan(&exists); err != nil {
			return fmt.Errorf("check run session: %w", err)
		}
		if exists == 0 {
			return storage.ErrNotFound
		}
		return storage.ErrSessionClosed
	}
	return nil
}

func (s *Store) GetRun(ctx context.Context, id string) (protocol.AgentRun, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, status, prompt, started_at, finished_at, exit_code
		FROM runs WHERE id = ?`, id)
	return scanRun(row)
}

func (s *Store) UpdateRun(ctx context.Context, run protocol.AgentRun) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE runs
		SET status = ?, started_at = ?, finished_at = ?, exit_code = ?
		WHERE id = ?`,
		run.Status,
		nullableTime(run.StartedAt),
		nullableTime(run.FinishedAt),
		nullableInt(run.ExitCode),
		run.ID,
	)
	return updateResult("update run", result, err)
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (protocol.AgentSession, error) {
	var session protocol.AgentSession
	var threadID, closedAt, cleanupError, cleanupUpdatedAt sql.NullString
	var createdAt string
	if err := row.Scan(
		&session.ID,
		&session.Agent,
		&session.Workspace,
		&session.Worktree,
		&session.BaseCommit,
		&threadID,
		&session.Status,
		&createdAt,
		&closedAt,
		&session.CleanupStatus,
		&cleanupError,
		&session.CleanupAttempts,
		&cleanupUpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.AgentSession{}, storage.ErrNotFound
		}
		return protocol.AgentSession{}, fmt.Errorf("scan session: %w", err)
	}
	parsedCreatedAt, err := parseTime(createdAt)
	if err != nil {
		return protocol.AgentSession{}, fmt.Errorf("scan session created_at: %w", err)
	}
	session.CreatedAt = parsedCreatedAt
	if threadID.Valid {
		session.CodexThreadID = &threadID.String
	}
	if closedAt.Valid {
		parsed, err := parseTime(closedAt.String)
		if err != nil {
			return protocol.AgentSession{}, fmt.Errorf("scan session closed_at: %w", err)
		}
		session.ClosedAt = &parsed
	}
	if cleanupError.Valid {
		session.CleanupError = &cleanupError.String
	}
	if cleanupUpdatedAt.Valid {
		parsed, err := parseTime(cleanupUpdatedAt.String)
		if err != nil {
			return protocol.AgentSession{}, fmt.Errorf("scan session cleanup_updated_at: %w", err)
		}
		session.CleanupUpdatedAt = &parsed
	}
	return session, nil
}

func scanRun(row scanner) (protocol.AgentRun, error) {
	var run protocol.AgentRun
	var startedAt, finishedAt sql.NullString
	var exitCode sql.NullInt64
	if err := row.Scan(
		&run.ID,
		&run.SessionID,
		&run.Status,
		&run.Prompt,
		&startedAt,
		&finishedAt,
		&exitCode,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.AgentRun{}, storage.ErrNotFound
		}
		return protocol.AgentRun{}, fmt.Errorf("scan run: %w", err)
	}
	var err error
	if run.StartedAt, err = parseNullableTime(startedAt); err != nil {
		return protocol.AgentRun{}, fmt.Errorf("scan run started_at: %w", err)
	}
	if run.FinishedAt, err = parseNullableTime(finishedAt); err != nil {
		return protocol.AgentRun{}, fmt.Errorf("scan run finished_at: %w", err)
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		run.ExitCode = &value
	}
	return run, nil
}

func updateResult(operation string, result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: rows affected: %w", operation, err)
	}
	if affected == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func parseNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
