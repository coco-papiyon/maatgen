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

// CreateTriggerRule persists a new GitHubTriggerRule (ADR-007 section 3).
func (s *Store) CreateTriggerRule(ctx context.Context, rule protocol.GitHubTriggerRule) error {
	eventKinds, filters, err := encodeTriggerRuleJSON(rule)
	if err != nil {
		return fmt.Errorf("create github trigger rule: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO github_trigger_rules(
		id, repository, name, enabled, event_kinds_json, filters_json, prompt_template, include_body,
		provider, model, reasoning_effort, auto_approve, concurrency_policy, priority, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.Repository, rule.Name, boolToInt(rule.Enabled), eventKinds, filters,
		rule.PromptTemplate, boolToInt(rule.IncludeBody), rule.Provider, nullableString(rule.Model),
		nullableString(rule.ReasoningEffort), boolToInt(rule.AutoApprove), rule.ConcurrencyPolicy, rule.Priority, formatTime(rule.CreatedAt), formatTime(rule.UpdatedAt))
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return storage.ErrConflict
		}
		return fmt.Errorf("create github trigger rule: %w", err)
	}
	return nil
}

// UpdateTriggerRule replaces every editable field of an existing rule.
func (s *Store) UpdateTriggerRule(ctx context.Context, rule protocol.GitHubTriggerRule) error {
	eventKinds, filters, err := encodeTriggerRuleJSON(rule)
	if err != nil {
		return fmt.Errorf("update github trigger rule: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE github_trigger_rules
		SET repository = ?, name = ?, enabled = ?, event_kinds_json = ?, filters_json = ?, prompt_template = ?, include_body = ?,
			provider = ?, model = ?, reasoning_effort = ?, auto_approve = ?, concurrency_policy = ?, priority = ?, updated_at = ?
		WHERE id = ?`,
		rule.Repository, rule.Name, boolToInt(rule.Enabled), eventKinds, filters, rule.PromptTemplate, boolToInt(rule.IncludeBody),
		rule.Provider, nullableString(rule.Model), nullableString(rule.ReasoningEffort), boolToInt(rule.AutoApprove),
		rule.ConcurrencyPolicy, rule.Priority, formatTime(rule.UpdatedAt), rule.ID)
	return updateResult("update github trigger rule", result, err)
}

// GetTriggerRule returns a single rule by ID, or storage.ErrNotFound.
func (s *Store) GetTriggerRule(ctx context.Context, id string) (protocol.GitHubTriggerRule, error) {
	return scanTriggerRule(s.db.QueryRowContext(ctx, triggerRuleSelect+` WHERE id = ?`, id))
}

// ListTriggerRules returns every rule configured for repository, in
// creation order.
func (s *Store) ListTriggerRules(ctx context.Context, repository string) ([]protocol.GitHubTriggerRule, error) {
	rows, err := s.db.QueryContext(ctx, triggerRuleSelect+` WHERE repository = ? ORDER BY created_at ASC, id ASC`, repository)
	if err != nil {
		return nil, fmt.Errorf("list github trigger rules: %w", err)
	}
	defer rows.Close()
	rules := []protocol.GitHubTriggerRule{}
	for rows.Next() {
		rule, err := scanTriggerRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list github trigger rules: %w", err)
	}
	return rules, nil
}

// ListAllTriggerRules returns every rule across every repository, ordered by
// repository then creation order. Used by the Settings screen's
// cross-repository rule table, where rules are managed independently of
// whichever repository is currently selected in the UI.
func (s *Store) ListAllTriggerRules(ctx context.Context) ([]protocol.GitHubTriggerRule, error) {
	rows, err := s.db.QueryContext(ctx, triggerRuleSelect+` ORDER BY repository ASC, created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list all github trigger rules: %w", err)
	}
	defer rows.Close()
	rules := []protocol.GitHubTriggerRule{}
	for rows.Next() {
		rule, err := scanTriggerRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list all github trigger rules: %w", err)
	}
	return rules, nil
}

// DeleteTriggerRule removes a rule. Existing monitor events created from it
// keep their history: github_monitor_events.rule_id is set to NULL rather
// than cascading the delete (ADR-007 section 6 requires event history to
// survive independently of rule/session/GitHub state).
func (s *Store) DeleteTriggerRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM github_trigger_rules WHERE id = ?`, id)
	return updateResult("delete github trigger rule", result, err)
}

const triggerRuleSelect = `SELECT id, repository, name, enabled, event_kinds_json, filters_json,
	prompt_template, include_body, provider, model, reasoning_effort, auto_approve, concurrency_policy, priority, created_at, updated_at
	FROM github_trigger_rules`

func encodeTriggerRuleJSON(rule protocol.GitHubTriggerRule) (eventKinds string, filters string, err error) {
	eventKindsData, err := json.Marshal(rule.EventKinds)
	if err != nil {
		return "", "", fmt.Errorf("encode event kinds: %w", err)
	}
	filtersData, err := json.Marshal(rule.Filters)
	if err != nil {
		return "", "", fmt.Errorf("encode filters: %w", err)
	}
	return string(eventKindsData), string(filtersData), nil
}

func scanTriggerRule(row scanner) (protocol.GitHubTriggerRule, error) {
	var rule protocol.GitHubTriggerRule
	var enabled, includeBody, autoApprove int
	var eventKinds, filters string
	var model, reasoningEffort sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(&rule.ID, &rule.Repository, &rule.Name, &enabled, &eventKinds, &filters,
		&rule.PromptTemplate, &includeBody, &rule.Provider, &model, &reasoningEffort, &autoApprove, &rule.ConcurrencyPolicy,
		&rule.Priority, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.GitHubTriggerRule{}, storage.ErrNotFound
		}
		return protocol.GitHubTriggerRule{}, fmt.Errorf("scan github trigger rule: %w", err)
	}
	rule.Enabled = enabled != 0
	rule.IncludeBody = includeBody != 0
	rule.AutoApprove = autoApprove != 0
	if err := json.Unmarshal([]byte(eventKinds), &rule.EventKinds); err != nil {
		return protocol.GitHubTriggerRule{}, fmt.Errorf("scan github trigger rule event kinds: %w", err)
	}
	if err := json.Unmarshal([]byte(filters), &rule.Filters); err != nil {
		return protocol.GitHubTriggerRule{}, fmt.Errorf("scan github trigger rule filters: %w", err)
	}
	if model.Valid {
		rule.Model = &model.String
	}
	if reasoningEffort.Valid {
		rule.ReasoningEffort = &reasoningEffort.String
	}
	var err error
	if rule.CreatedAt, err = parseTime(createdAt); err != nil {
		return protocol.GitHubTriggerRule{}, fmt.Errorf("scan github trigger rule created_at: %w", err)
	}
	if rule.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return protocol.GitHubTriggerRule{}, fmt.Errorf("scan github trigger rule updated_at: %w", err)
	}
	return rule, nil
}
