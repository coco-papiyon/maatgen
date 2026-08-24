package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

// UpsertObservedItem records the most recently observed normalized state of
// one Issue or Pull Request, replacing whatever was previously recorded for
// the same (repository, kind, number). This is the diff basis the next
// poll compares against (ADR-007 section 3); it is monitoring-only state
// and must never be read back as an Issue/PR list or detail response.
func (s *Store) UpsertObservedItem(ctx context.Context, item protocol.GitHubObservedItem) error {
	return upsertObservedItem(ctx, s.db, item)
}

func upsertObservedItem(ctx context.Context, exec execer, item protocol.GitHubObservedItem) error {
	snapshot, err := json.Marshal(item.Snapshot)
	if err != nil {
		return fmt.Errorf("upsert github observed item: encode snapshot: %w", err)
	}
	evaluatedRuleVersions, err := json.Marshal(item.EvaluatedRuleVersions)
	if err != nil {
		return fmt.Errorf("upsert github observed item: encode evaluated rule versions: %w", err)
	}
	_, err = exec.ExecContext(ctx, `INSERT INTO github_observed_items(
		repository, kind, number, state_hash, last_action, projects_available, snapshot_json,
		evaluated_rule_versions_json, first_synced_at, observed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(repository, kind, number) DO UPDATE SET
		state_hash = excluded.state_hash,
		last_action = excluded.last_action,
		projects_available = excluded.projects_available,
		snapshot_json = excluded.snapshot_json,
		evaluated_rule_versions_json = excluded.evaluated_rule_versions_json,
		observed_at = excluded.observed_at`,
		item.Repository, item.Kind, item.Number, item.StateHash, item.LastAction,
		boolToInt(item.ProjectsAvailable), string(snapshot), string(evaluatedRuleVersions),
		formatTime(item.FirstSyncedAt), formatTime(item.ObservedAt))
	if err != nil {
		return fmt.Errorf("upsert github observed item: %w", err)
	}
	return nil
}

// GetObservedItem returns the last observed state of one item, or
// storage.ErrNotFound if the item has never been observed (the first-sync
// case: ADR-007 section 3 requires that case be treated as "establish a
// baseline," not "everything changed").
func (s *Store) GetObservedItem(ctx context.Context, repository string, kind protocol.GitHubItemKind, number int) (protocol.GitHubObservedItem, error) {
	return scanObservedItem(s.db.QueryRowContext(ctx,
		observedItemSelect+` WHERE repository = ? AND kind = ? AND number = ?`, repository, kind, number))
}

// ListObservedItems returns every item observed for repository.
func (s *Store) ListObservedItems(ctx context.Context, repository string) ([]protocol.GitHubObservedItem, error) {
	rows, err := s.db.QueryContext(ctx, observedItemSelect+` WHERE repository = ? ORDER BY kind ASC, number ASC`, repository)
	if err != nil {
		return nil, fmt.Errorf("list github observed items: %w", err)
	}
	defer rows.Close()
	items := []protocol.GitHubObservedItem{}
	for rows.Next() {
		item, err := scanObservedItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list github observed items: %w", err)
	}
	return items, nil
}

// ClearRepositoryObservations deletes all observed items for a repository
// when its GitHub remote has changed (ADR-007 section 1: remote変更時に旧観測状態を破棄).
func (s *Store) ClearRepositoryObservations(ctx context.Context, repository string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM github_observed_items WHERE repository = ?`, repository)
	if err != nil {
		return fmt.Errorf("clear github observed items: %w", err)
	}
	return nil
}

const observedItemSelect = `SELECT repository, kind, number, state_hash, last_action,
	projects_available, snapshot_json, evaluated_rule_versions_json, first_synced_at, observed_at FROM github_observed_items`

func scanObservedItem(row scanner) (protocol.GitHubObservedItem, error) {
	var item protocol.GitHubObservedItem
	var projectsAvailable int
	var snapshot, evaluatedRuleVersions string
	var firstSyncedAt, observedAt string
	if err := row.Scan(&item.Repository, &item.Kind, &item.Number, &item.StateHash, &item.LastAction,
		&projectsAvailable, &snapshot, &evaluatedRuleVersions, &firstSyncedAt, &observedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.GitHubObservedItem{}, storage.ErrNotFound
		}
		return protocol.GitHubObservedItem{}, fmt.Errorf("scan github observed item: %w", err)
	}
	item.ProjectsAvailable = projectsAvailable != 0
	if err := json.Unmarshal([]byte(snapshot), &item.Snapshot); err != nil {
		return protocol.GitHubObservedItem{}, fmt.Errorf("scan github observed item snapshot: %w", err)
	}
	if err := json.Unmarshal([]byte(evaluatedRuleVersions), &item.EvaluatedRuleVersions); err != nil {
		return protocol.GitHubObservedItem{}, fmt.Errorf("scan github observed item evaluated rule versions: %w", err)
	}
	var err error
	if item.FirstSyncedAt, err = parseTime(firstSyncedAt); err != nil {
		return protocol.GitHubObservedItem{}, fmt.Errorf("scan github observed item first_synced_at: %w", err)
	}
	if item.ObservedAt, err = parseTime(observedAt); err != nil {
		return protocol.GitHubObservedItem{}, fmt.Errorf("scan github observed item observed_at: %w", err)
	}
	return item, nil
}
