package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func (s *Store) CreateCheckpoint(ctx context.Context, checkpoint protocol.Checkpoint) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO checkpoints(
		id, session_id, run_id, head_commit, index_tree, before_tree, before_ref, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, checkpoint.ID, checkpoint.SessionID, checkpoint.RunID,
		checkpoint.HeadCommit, checkpoint.IndexTree, checkpoint.BeforeTree, checkpoint.BeforeRef,
		formatTime(checkpoint.CreatedAt))
	if err != nil {
		return fmt.Errorf("create checkpoint: %w", err)
	}
	return nil
}

func (s *Store) CompleteCheckpoint(ctx context.Context, id, afterTree, afterRef string, completedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE checkpoints SET after_tree = ?, after_ref = ?, completed_at = ? WHERE id = ?`,
		afterTree, afterRef, formatTime(completedAt), id)
	return updateResult("complete checkpoint", result, err)
}

func (s *Store) GetCheckpoint(ctx context.Context, sessionID, checkpointID string) (protocol.Checkpoint, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, session_id, run_id, head_commit, index_tree,
		before_tree, after_tree, before_ref, after_ref, created_at, completed_at
		FROM checkpoints WHERE session_id = ? AND id = ?`, sessionID, checkpointID)
	return scanCheckpoint(row)
}

func scanCheckpoint(row scanner) (protocol.Checkpoint, error) {
	var result protocol.Checkpoint
	var afterTree, afterRef, completedAt sql.NullString
	var createdAt string
	if err := row.Scan(&result.ID, &result.SessionID, &result.RunID, &result.HeadCommit,
		&result.IndexTree, &result.BeforeTree, &afterTree, &result.BeforeRef, &afterRef,
		&createdAt, &completedAt); err != nil {
		if err == sql.ErrNoRows {
			return protocol.Checkpoint{}, storage.ErrNotFound
		}
		return protocol.Checkpoint{}, fmt.Errorf("scan checkpoint: %w", err)
	}
	var err error
	if result.CreatedAt, err = parseTime(createdAt); err != nil {
		return protocol.Checkpoint{}, err
	}
	if afterTree.Valid {
		result.AfterTree = &afterTree.String
	}
	if afterRef.Valid {
		result.AfterRef = &afterRef.String
	}
	if result.CompletedAt, err = parseNullableTime(completedAt); err != nil {
		return protocol.Checkpoint{}, err
	}
	return result, nil
}

func (s *Store) ReplaceChangeSet(ctx context.Context, changeSet protocol.ChangeSet) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace change set: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM changes WHERE checkpoint_id = ?", changeSet.CheckpointID); err != nil {
		return fmt.Errorf("clear change set: %w", err)
	}
	createdAt := formatTime(time.Now().UTC())
	for fileOrder, file := range changeSet.Files {
		if len(file.Hunks) == 0 {
			if err := insertChangeRow(ctx, tx, changeSet, file, nil, fileOrder, 0, createdAt); err != nil {
				return err
			}
			continue
		}
		for hunkOrder := range file.Hunks {
			if err := insertChangeRow(ctx, tx, changeSet, file, &file.Hunks[hunkOrder], fileOrder, hunkOrder, createdAt); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit change set: %w", err)
	}
	return nil
}

func insertChangeRow(ctx context.Context, tx *sql.Tx, set protocol.ChangeSet, file protocol.FileChange, hunk *protocol.ChangeHunk, fileOrder, hunkOrder int, createdAt string) error {
	var hunkID, oldStart, oldLines, newStart, newLines, originalText, modifiedText, hunkStatus any
	if hunk != nil {
		hunkID = hunk.ID
		oldStart = hunk.OldStart
		oldLines = hunk.OldLines
		newStart = hunk.NewStart
		newLines = hunk.NewLines
		originalText = hunk.OriginalText
		modifiedText = hunk.ModifiedText
		hunkStatus = hunk.Status
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO changes(
		session_id, run_id, checkpoint_id, file_id, hunk_id, old_path, new_path,
		change_kind, restore_mode, old_start, old_lines, new_start, new_lines,
		original_text, modified_text, original_file, modified_file, file_status,
		hunk_status, file_order, hunk_order, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		set.SessionID, set.RunID, set.CheckpointID, file.ID, hunkID,
		nullablePointer(file.OldPath), nullablePointer(file.NewPath), file.Kind, file.RestoreMode,
		oldStart, oldLines, newStart, newLines, originalText, modifiedText,
		nullablePointer(file.Original), nullablePointer(file.Modified), file.Status, hunkStatus,
		fileOrder, hunkOrder, createdAt)
	if err != nil {
		return fmt.Errorf("insert change %q: %w", file.ID, err)
	}
	return nil
}

func (s *Store) GetChangeSet(ctx context.Context, sessionID string) (protocol.ChangeSet, error) {
	var checkpointID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM checkpoints WHERE session_id = ? AND after_tree IS NOT NULL ORDER BY created_at DESC LIMIT 1`, sessionID).Scan(&checkpointID)
	if err == sql.ErrNoRows {
		if _, getErr := s.GetSession(ctx, sessionID); getErr != nil {
			return protocol.ChangeSet{}, getErr
		}
		return protocol.ChangeSet{SessionID: sessionID, Files: []protocol.FileChange{}}, nil
	}
	if err != nil {
		return protocol.ChangeSet{}, fmt.Errorf("find latest checkpoint: %w", err)
	}
	return s.GetChangeSetForCheckpoint(ctx, sessionID, checkpointID)
}

func (s *Store) GetChangeSetForCheckpoint(ctx context.Context, sessionID, checkpointID string) (protocol.ChangeSet, error) {
	cp, err := s.GetCheckpoint(ctx, sessionID, checkpointID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	if cp.AfterTree == nil {
		return protocol.ChangeSet{}, storage.ErrNotFound
	}
	result := protocol.ChangeSet{SessionID: sessionID, RunID: cp.RunID, CheckpointID: cp.ID,
		BeforeTree: cp.BeforeTree, AfterTree: *cp.AfterTree, Files: []protocol.FileChange{}}
	rows, err := s.db.QueryContext(ctx, `SELECT file_id, hunk_id, old_path, new_path, change_kind,
		restore_mode, original_file, modified_file, file_status, old_start, old_lines,
		new_start, new_lines, original_text, modified_text, hunk_status
		FROM changes WHERE checkpoint_id = ? ORDER BY file_order, hunk_order`, checkpointID)
	if err != nil {
		return protocol.ChangeSet{}, fmt.Errorf("query change set: %w", err)
	}
	defer rows.Close()
	indexes := map[string]int{}
	for rows.Next() {
		var fileID string
		var hunkID, oldPath, newPath, originalFile, modifiedFile sql.NullString
		var kind protocol.FileChangeKind
		var restoreMode string
		var fileStatus protocol.RestoreStatus
		var oldStart, oldLines, newStart, newLines sql.NullInt64
		var originalText, modifiedText, hunkStatus sql.NullString
		if err := rows.Scan(&fileID, &hunkID, &oldPath, &newPath, &kind, &restoreMode,
			&originalFile, &modifiedFile, &fileStatus, &oldStart, &oldLines, &newStart,
			&newLines, &originalText, &modifiedText, &hunkStatus); err != nil {
			return protocol.ChangeSet{}, err
		}
		idx, ok := indexes[fileID]
		if !ok {
			idx = len(result.Files)
			indexes[fileID] = idx
			result.Files = append(result.Files, protocol.FileChange{ID: fileID, OldPath: nullStringPointer(oldPath), NewPath: nullStringPointer(newPath), Kind: kind,
				Original: nullStringPointer(originalFile), Modified: nullStringPointer(modifiedFile), RestoreMode: restoreMode, Status: fileStatus, Hunks: []protocol.ChangeHunk{}})
		}
		if hunkID.Valid {
			status := protocol.RestoreChanged
			if hunkStatus.Valid {
				status = protocol.RestoreStatus(hunkStatus.String)
			}
			result.Files[idx].Hunks = append(result.Files[idx].Hunks, protocol.ChangeHunk{ID: hunkID.String,
				OldStart: int(oldStart.Int64), OldLines: int(oldLines.Int64), NewStart: int(newStart.Int64), NewLines: int(newLines.Int64),
				OriginalText: originalText.String, ModifiedText: modifiedText.String, Status: status})
		}
	}
	return result, rows.Err()
}

func (s *Store) UpdateHunkRestore(ctx context.Context, checkpointID, hunkID string, status protocol.RestoreStatus, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var fileID string
	if err := tx.QueryRowContext(ctx, `SELECT file_id FROM changes WHERE checkpoint_id = ? AND hunk_id = ?`, checkpointID, hunkID).Scan(&fileID); err != nil {
		if err == sql.ErrNoRows {
			return storage.ErrNotFound
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE changes SET hunk_status = ?, restored_at = ? WHERE checkpoint_id = ? AND hunk_id = ?`, status, formatTime(at), checkpointID, hunkID); err != nil {
		return err
	}
	var total, restored, conflicts int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), SUM(hunk_status='restored'), SUM(hunk_status='conflict') FROM changes WHERE checkpoint_id = ? AND file_id = ? AND hunk_id IS NOT NULL`, checkpointID, fileID).Scan(&total, &restored, &conflicts); err != nil {
		return err
	}
	fileStatus := protocol.RestoreChanged
	if restored == total {
		fileStatus = protocol.RestoreRestored
	} else if restored > 0 {
		fileStatus = protocol.RestorePartiallyRestored
	} else if conflicts > 0 {
		fileStatus = protocol.RestoreConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE changes SET file_status = ? WHERE checkpoint_id = ? AND file_id = ?`, fileStatus, checkpointID, fileID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UpdateFileRestore(ctx context.Context, checkpointID, fileID string, status protocol.RestoreStatus, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE changes SET file_status = ?, hunk_status = CASE WHEN hunk_id IS NULL THEN NULL ELSE ? END, restored_at = ? WHERE checkpoint_id = ? AND file_id = ?`, status, status, formatTime(at), checkpointID, fileID)
	return updateResult("update file restore", result, err)
}

func nullablePointer(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
