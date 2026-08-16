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

func (s *Store) CreateApproval(ctx context.Context, approval protocol.CommandApproval) error {
	segments, err := json.Marshal(approval.Segments)
	if err != nil {
		return fmt.Errorf("create approval: encode segments: %w", err)
	}
	factors, err := json.Marshal(approval.Factors)
	if err != nil {
		return fmt.Errorf("create approval: encode factors: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO command_approvals(
		id, session_id, run_id, provider_request_id, command, shell, working_directory,
		segments_json, status, risk, confidence, summary, factors_json, decision, scope,
		source, rule_argv_json, created_at, decided_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		approval.ID, approval.SessionID, approval.RunID, approval.ProviderRequestID,
		approval.Command, approval.Shell, approval.WorkingDirectory, string(segments),
		approval.Status, nullableApprovalRisk(approval.Risk), nullableFloat64(approval.Confidence),
		nullableText(approval.Summary), string(factors), nullableApprovalDecision(approval.Decision),
		nullableApprovalScope(approval.Scope), nullableApprovalSource(approval.Source),
		nullableJSONStrings(approval.RuleArgv), formatTime(approval.CreatedAt), nullableTime(approval.DecidedAt))
	if err != nil {
		return fmt.Errorf("create approval: %w", err)
	}
	return nil
}

func (s *Store) UpdateApprovalAssessment(ctx context.Context, id string, risk protocol.ApprovalRisk, confidence float64, summary string, factors []string) error {
	encoded, err := json.Marshal(factors)
	if err != nil {
		return fmt.Errorf("update approval assessment: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE command_approvals
		SET risk = ?, confidence = ?, summary = ?, factors_json = ?
		WHERE id = ? AND status = 'pending'`, risk, confidence, summary, string(encoded), id)
	return approvalUpdateResult("update approval assessment", result, err)
}

func (s *Store) DecideApproval(ctx context.Context, id string, status protocol.ApprovalStatus, decision protocol.ApprovalDecision, scope protocol.ApprovalScope, source protocol.ApprovalSource, ruleArgv []string, decidedAt time.Time) (protocol.CommandApproval, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE command_approvals
		SET status = ?, decision = ?, scope = ?, source = ?, rule_argv_json = ?, decided_at = ?
		WHERE id = ? AND status = 'pending'`, status, decision, scope, source,
		nullableJSONStrings(ruleArgv), formatTime(decidedAt), id)
	if err := approvalUpdateResult("decide approval", result, err); err != nil {
		return protocol.CommandApproval{}, err
	}
	return s.GetApproval(ctx, id)
}

func (s *Store) GetApproval(ctx context.Context, id string) (protocol.CommandApproval, error) {
	return scanApproval(s.db.QueryRowContext(ctx, approvalSelect+` WHERE id = ?`, id))
}

func (s *Store) ListApprovals(ctx context.Context, sessionID string, status *protocol.ApprovalStatus) ([]protocol.CommandApproval, error) {
	query := approvalSelect + ` WHERE session_id = ?`
	args := []any{sessionID}
	if status != nil {
		query += ` AND status = ?`
		args = append(args, *status)
	}
	query += ` ORDER BY created_at ASC, id ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	defer rows.Close()
	approvals := []protocol.CommandApproval{}
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, approval)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	return approvals, nil
}

func (s *Store) ExpirePendingApprovals(ctx context.Context, decidedAt time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE command_approvals
		SET status = 'expired', decision = 'deny', scope = 'once', source = 'system', decided_at = ?
		WHERE status = 'pending'`, formatTime(decidedAt))
	if err != nil {
		return 0, fmt.Errorf("expire pending approvals: %w", err)
	}
	return result.RowsAffected()
}

const approvalSelect = `SELECT id, session_id, run_id, provider_request_id, command, shell,
	working_directory, segments_json, status, risk, confidence, summary, factors_json,
	decision, scope, source, rule_argv_json, created_at, decided_at FROM command_approvals`

func scanApproval(row scanner) (protocol.CommandApproval, error) {
	var approval protocol.CommandApproval
	var segments, factors string
	var risk, summary, decision, scope, source, ruleArgv, decidedAt sql.NullString
	var confidence sql.NullFloat64
	var createdAt string
	if err := row.Scan(&approval.ID, &approval.SessionID, &approval.RunID, &approval.ProviderRequestID,
		&approval.Command, &approval.Shell, &approval.WorkingDirectory, &segments, &approval.Status,
		&risk, &confidence, &summary, &factors, &decision, &scope, &source, &ruleArgv,
		&createdAt, &decidedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return protocol.CommandApproval{}, storage.ErrNotFound
		}
		return protocol.CommandApproval{}, fmt.Errorf("scan approval: %w", err)
	}
	if err := json.Unmarshal([]byte(segments), &approval.Segments); err != nil {
		return protocol.CommandApproval{}, fmt.Errorf("scan approval segments: %w", err)
	}
	if err := json.Unmarshal([]byte(factors), &approval.Factors); err != nil {
		return protocol.CommandApproval{}, fmt.Errorf("scan approval factors: %w", err)
	}
	if ruleArgv.Valid {
		if err := json.Unmarshal([]byte(ruleArgv.String), &approval.RuleArgv); err != nil {
			return protocol.CommandApproval{}, fmt.Errorf("scan approval rule argv: %w", err)
		}
	}
	if risk.Valid {
		value := protocol.ApprovalRisk(risk.String)
		approval.Risk = &value
	}
	if confidence.Valid {
		approval.Confidence = &confidence.Float64
	}
	if summary.Valid {
		approval.Summary = &summary.String
	}
	if decision.Valid {
		value := protocol.ApprovalDecision(decision.String)
		approval.Decision = &value
	}
	if scope.Valid {
		value := protocol.ApprovalScope(scope.String)
		approval.Scope = &value
	}
	if source.Valid {
		value := protocol.ApprovalSource(source.String)
		approval.Source = &value
	}
	parsed, err := parseTime(createdAt)
	if err != nil {
		return protocol.CommandApproval{}, fmt.Errorf("scan approval created_at: %w", err)
	}
	approval.CreatedAt = parsed
	if decidedAt.Valid {
		parsed, err := parseTime(decidedAt.String)
		if err != nil {
			return protocol.CommandApproval{}, fmt.Errorf("scan approval decided_at: %w", err)
		}
		approval.DecidedAt = &parsed
	}
	return approval, nil
}

func approvalUpdateResult(operation string, result sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if affected == 0 {
		return storage.ErrConflict
	}
	return nil
}

func nullableApprovalRisk(value *protocol.ApprovalRisk) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableApprovalDecision(value *protocol.ApprovalDecision) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableApprovalScope(value *protocol.ApprovalScope) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableApprovalSource(value *protocol.ApprovalSource) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullableJSONStrings(value []string) any {
	if len(value) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
