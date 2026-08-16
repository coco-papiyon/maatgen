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
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = OFF",
		// Migrations that rebuild a parent table keep child foreign keys bound
		// to the final table name rather than following the temporary rename.
		"PRAGMA legacy_alter_table = ON",
	} {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		_, _ = s.db.ExecContext(context.WithoutCancel(ctx), "PRAGMA legacy_alter_table = OFF")
		_, _ = s.db.ExecContext(context.WithoutCancel(ctx), "PRAGMA foreign_keys = ON")
		return err
	}
	var foreignKeyViolation string
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE((SELECT `table` FROM pragma_foreign_key_check LIMIT 1), '')").Scan(&foreignKeyViolation); err != nil {
		return fmt.Errorf("check migrated sqlite foreign keys: %w", err)
	}
	if foreignKeyViolation != "" {
		return fmt.Errorf("check migrated sqlite foreign keys: table %s has an invalid reference", foreignKeyViolation)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA legacy_alter_table = OFF"); err != nil {
		return fmt.Errorf("restore sqlite alter-table behavior: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	return nil
}

// DeleteEmptySessions removes sessions that have no runs and no events.
// Returns the number of deleted sessions.
func (s *Store) DeleteEmptySessions(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM sessions WHERE id IN (
			SELECT s.id FROM sessions s
			LEFT JOIN runs r ON r.session_id = s.id
			LEFT JOIN events e ON e.session_id = s.id
			WHERE r.id IS NULL AND e.id IS NULL
		)`)
	if err != nil {
		return 0, fmt.Errorf("delete empty sessions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete empty sessions rows affected: %w", err)
	}
	return affected, nil
}

func (s *Store) FailInterruptedRuns(ctx context.Context, finishedAt time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE runs SET status = 'failed', finished_at = COALESCE(finished_at, ?)
		WHERE status IN ('queued', 'starting', 'running', 'waiting_for_approval')`, formatTime(finishedAt))
	if err != nil {
		return 0, fmt.Errorf("fail interrupted runs: %w", err)
	}
	return result.RowsAffected()
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(
			id, agent, workspace, agent_thread_id, status, created_at, closed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.Agent,
		session.Workspace,
		nullableString(session.AgentThreadID),
		session.Status,
		formatTime(session.CreatedAt),
		nullableTime(session.ClosedAt),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) GetSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, agent, workspace, agent_thread_id, status, created_at, closed_at
		FROM sessions WHERE id = ?`, id)
	return scanSession(row)
}

func (s *Store) ListSessions(ctx context.Context, limit int, before *protocol.SessionCursor) ([]protocol.AgentSession, error) {
	if limit <= 0 || limit > 501 {
		limit = 100
	}
	query := `
		SELECT id, agent, workspace, agent_thread_id, status, created_at, closed_at
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
		"UPDATE sessions SET agent_thread_id = ? WHERE id = ?", threadID, id,
	)
	return updateResult("update session thread", result, err)
}

func (s *Store) CloseSession(ctx context.Context, id string, closedAt time.Time) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status = ?, closed_at = COALESCE(closed_at, ?)
		 WHERE id = ? AND NOT EXISTS (
		   SELECT 1 FROM runs WHERE session_id = ? AND status IN ('queued', 'starting', 'running', 'waiting_for_approval')
		 )`,
		protocol.SessionClosed, formatTime(closedAt), id, id,
	)
	if err != nil {
		return fmt.Errorf("close session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var exists, active int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return storage.ErrNotFound
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE session_id = ? AND status IN ('queued','starting','running','waiting_for_approval')`, id).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return storage.ErrRunActive
		}
	}
	return nil
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
	var threadID, closedAt sql.NullString
	var createdAt string
	if err := row.Scan(
		&session.ID,
		&session.Agent,
		&session.Workspace,
		&threadID,
		&session.Status,
		&createdAt,
		&closedAt,
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
		session.AgentThreadID = &threadID.String
	}
	if closedAt.Valid {
		parsed, err := parseTime(closedAt.String)
		if err != nil {
			return protocol.AgentSession{}, fmt.Errorf("scan session closed_at: %w", err)
		}
		session.ClosedAt = &parsed
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
