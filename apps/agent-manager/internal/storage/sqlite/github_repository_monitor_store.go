package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

// CreateRepositoryMonitor creates the GitHub polling configuration for a
// local repository (ADR-007 section 3). Repository is a natural key: a
// repository already registered returns storage.ErrConflict.
func (s *Store) CreateRepositoryMonitor(ctx context.Context, monitor protocol.GitHubRepositoryMonitor) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO github_repository_monitors(
		repository, host, owner, name, remote_name, project_name, enabled, poll_interval_seconds,
		coalesce_queue_limit, last_synced_at, next_sync_at, last_error, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		monitor.Repository, monitor.Host, monitor.Owner, monitor.Name, monitor.RemoteName, monitor.ProjectName,
		boolToInt(monitor.Enabled), monitor.PollIntervalSeconds, monitor.CoalesceQueueLimit,
		nullableTime(monitor.LastSyncedAt), nullableTime(monitor.NextSyncAt), nullableString(monitor.LastError),
		formatTime(monitor.CreatedAt), formatTime(monitor.UpdatedAt))
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("create github repository monitor: %w", err)
	}
	return nil
}

// UpdateRepositoryMonitorSettings updates the user-editable configuration of
// a monitor: which remote it targets (host/owner/name/remoteName may change
// if the local repository's remotes change), whether it is enabled, the
// poll interval, and the coalesce queue limit (ADR-007 section 6).
func (s *Store) UpdateRepositoryMonitorSettings(ctx context.Context, monitor protocol.GitHubRepositoryMonitor, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE github_repository_monitors
		SET host = ?, owner = ?, name = ?, remote_name = ?, project_name = ?, enabled = ?,
			poll_interval_seconds = ?, coalesce_queue_limit = ?, updated_at = ?
		WHERE repository = ?`,
		monitor.Host, monitor.Owner, monitor.Name, monitor.RemoteName, monitor.ProjectName, boolToInt(monitor.Enabled),
		monitor.PollIntervalSeconds, monitor.CoalesceQueueLimit, formatTime(updatedAt), monitor.Repository)
	return updateResult("update github repository monitor settings", result, err)
}

// UpdateRepositoryMonitorSyncState records the outcome of a poll cycle:
// when it last ran, when it should next run, and the last error (if any).
// A successful poll should pass lastError as nil.
func (s *Store) UpdateRepositoryMonitorSyncState(ctx context.Context, repository string, lastSyncedAt, nextSyncAt time.Time, lastError *string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE github_repository_monitors
		SET last_synced_at = ?, next_sync_at = ?, last_error = ?, updated_at = ?
		WHERE repository = ?`,
		formatTime(lastSyncedAt), formatTime(nextSyncAt), nullableString(lastError), formatTime(updatedAt), repository)
	return updateResult("update github repository monitor sync state", result, err)
}

// ApplyRemoteChange atomically applies a detected GitHub remote change
// (ADR-007 section 1) to a monitor: it updates the monitor's target
// (host/owner/name/remoteName), clears its previously observed items, and
// resets its sync state (last_synced_at/next_sync_at) to nil, all in a
// single transaction.
//
// Atomicity matters here specifically because these three effects must
// never be observed partially: if the monitor's target were updated but the
// crash happened before observations were cleared or sync state reset, a
// restart would see a monitor already pointing at the new repository (so it
// would never take the "remote changed" branch again) while still holding
// the old repository's observed items and a non-nil LastSyncedAt — causing
// the new target's items to be diffed against the old target's state and
// producing spurious "changed" events (ADR-007 section 3). Doing all three
// in one transaction rules that out: either the whole switch is visible, or
// none of it is, and an interrupted switch is retried in full on the next
// sync attempt.
func (s *Store) ApplyRemoteChange(ctx context.Context, monitor protocol.GitHubRepositoryMonitor, updatedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("apply github remote change: begin transaction: %w", err)
	}

	result, err := tx.ExecContext(ctx, `UPDATE github_repository_monitors
		SET host = ?, owner = ?, name = ?, remote_name = ?,
			last_synced_at = NULL, next_sync_at = NULL, updated_at = ?
		WHERE repository = ?`,
		monitor.Host, monitor.Owner, monitor.Name, monitor.RemoteName, formatTime(updatedAt), monitor.Repository)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply github remote change: update monitor: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		_ = tx.Rollback()
		return storage.ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM github_observed_items WHERE repository = ?`, monitor.Repository); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply github remote change: clear observations: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("apply github remote change: commit: %w", err)
	}
	return nil
}

// GetRepositoryMonitor returns the monitor for repository, or
// storage.ErrNotFound if none is registered.
func (s *Store) GetRepositoryMonitor(ctx context.Context, repository string) (protocol.GitHubRepositoryMonitor, error) {
	return scanRepositoryMonitor(s.db.QueryRowContext(ctx, repositoryMonitorSelect+` WHERE repository = ?`, repository))
}

// ListRepositoryMonitors returns every registered monitor, ordered by
// repository path for stable output.
func (s *Store) ListRepositoryMonitors(ctx context.Context) ([]protocol.GitHubRepositoryMonitor, error) {
	rows, err := s.db.QueryContext(ctx, repositoryMonitorSelect+` ORDER BY repository ASC`)
	if err != nil {
		return nil, fmt.Errorf("list github repository monitors: %w", err)
	}
	defer rows.Close()
	monitors := []protocol.GitHubRepositoryMonitor{}
	for rows.Next() {
		monitor, err := scanRepositoryMonitor(rows)
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, monitor)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list github repository monitors: %w", err)
	}
	return monitors, nil
}

// DeleteRepositoryMonitor removes a repository's monitor along with its
// trigger rules, observed items, and monitor events (all reference
// repository with ON DELETE CASCADE).
func (s *Store) DeleteRepositoryMonitor(ctx context.Context, repository string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM github_repository_monitors WHERE repository = ?`, repository)
	return updateResult("delete github repository monitor", result, err)
}

const repositoryMonitorSelect = `SELECT repository, host, owner, name, remote_name, project_name, enabled,
	poll_interval_seconds, coalesce_queue_limit, last_synced_at, next_sync_at, last_error,
	created_at, updated_at FROM github_repository_monitors`

func scanRepositoryMonitor(row scanner) (protocol.GitHubRepositoryMonitor, error) {
	var monitor protocol.GitHubRepositoryMonitor
	var enabled int
	var lastSyncedAt, nextSyncAt, lastError sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(&monitor.Repository, &monitor.Host, &monitor.Owner, &monitor.Name, &monitor.RemoteName, &monitor.ProjectName,
		&enabled, &monitor.PollIntervalSeconds, &monitor.CoalesceQueueLimit, &lastSyncedAt, &nextSyncAt, &lastError,
		&createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.GitHubRepositoryMonitor{}, storage.ErrNotFound
		}
		return protocol.GitHubRepositoryMonitor{}, fmt.Errorf("scan github repository monitor: %w", err)
	}
	monitor.Enabled = enabled != 0
	var err error
	if monitor.LastSyncedAt, err = parseNullableTime(lastSyncedAt); err != nil {
		return protocol.GitHubRepositoryMonitor{}, fmt.Errorf("scan github repository monitor last_synced_at: %w", err)
	}
	if monitor.NextSyncAt, err = parseNullableTime(nextSyncAt); err != nil {
		return protocol.GitHubRepositoryMonitor{}, fmt.Errorf("scan github repository monitor next_sync_at: %w", err)
	}
	if lastError.Valid {
		monitor.LastError = &lastError.String
	}
	if monitor.CreatedAt, err = parseTime(createdAt); err != nil {
		return protocol.GitHubRepositoryMonitor{}, fmt.Errorf("scan github repository monitor created_at: %w", err)
	}
	if monitor.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return protocol.GitHubRepositoryMonitor{}, fmt.Errorf("scan github repository monitor updated_at: %w", err)
	}
	return monitor, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// isUniqueConstraintViolation reports whether err came from a SQLite UNIQUE
// (or PRIMARY KEY) constraint failure. Matching on the driver's message
// text mirrors how this package already distinguishes specific SQLite
// conditions (see checkpoint.isLockContention).
func isUniqueConstraintViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
