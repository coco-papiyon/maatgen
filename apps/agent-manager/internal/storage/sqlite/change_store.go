package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func (s *Store) ReplaceChangeSet(ctx context.Context, changeSet protocol.ChangeSet) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace change set: %w", err)
	}
	defer tx.Rollback()

	var sessionExists int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", changeSet.SessionID).Scan(&sessionExists); err != nil {
		return fmt.Errorf("check change set session: %w", err)
	}
	if sessionExists == 0 {
		return storage.ErrNotFound
	}
	preservedHunks := make(map[string]protocol.ReviewStatus)
	preservedFiles := make(map[string]protocol.ReviewStatus)
	rows, err := tx.QueryContext(ctx, `
		SELECT file_id, hunk_id, file_status, hunk_status
		FROM changes WHERE session_id = ? AND file_id IS NOT NULL`, changeSet.SessionID)
	if err != nil {
		return fmt.Errorf("read existing review state: %w", err)
	}
	for rows.Next() {
		var fileID string
		var hunkID, hunkStatus sql.NullString
		var fileStatus protocol.ReviewStatus
		if err := rows.Scan(&fileID, &hunkID, &fileStatus, &hunkStatus); err != nil {
			rows.Close()
			return fmt.Errorf("scan existing review state: %w", err)
		}
		preservedFiles[fileID] = fileStatus
		if hunkID.Valid && hunkStatus.Valid {
			preservedHunks[hunkID.String] = protocol.ReviewStatus(hunkStatus.String)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close existing review state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM changes WHERE session_id = ?", changeSet.SessionID); err != nil {
		return fmt.Errorf("clear change set: %w", err)
	}

	createdAt := formatTime(time.Now().UTC())
	for fileOrder, file := range changeSet.Files {
		if len(file.Hunks) == 0 {
			if preserved, exists := preservedFiles[file.ID]; exists {
				file.Status = preserved
			}
		} else {
			for index := range file.Hunks {
				if preserved, exists := preservedHunks[file.Hunks[index].ID]; exists {
					file.Hunks[index].Status = preserved
				}
			}
			file.Status = summarizeReview(file.Hunks)
		}
		if len(file.Hunks) == 0 {
			if err := insertChangeRow(ctx, tx, changeSet.SessionID, file, nil, fileOrder, 0, createdAt); err != nil {
				return err
			}
			continue
		}
		for hunkOrder := range file.Hunks {
			hunk := &file.Hunks[hunkOrder]
			if err := insertChangeRow(ctx, tx, changeSet.SessionID, file, hunk, fileOrder, hunkOrder, createdAt); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit change set: %w", err)
	}
	return nil
}

func summarizeReview(hunks []protocol.ChangeHunk) protocol.ReviewStatus {
	accepted, rejected, pending := 0, 0, 0
	for _, hunk := range hunks {
		switch hunk.Status {
		case protocol.ReviewAccepted:
			accepted++
		case protocol.ReviewRejected:
			rejected++
		default:
			pending++
		}
	}
	switch {
	case accepted == len(hunks):
		return protocol.ReviewAccepted
	case rejected == len(hunks):
		return protocol.ReviewRejected
	case accepted > 0:
		return protocol.ReviewPartiallyAccepted
	case pending == 0:
		return protocol.ReviewRejected
	default:
		return protocol.ReviewPending
	}
}

func insertChangeRow(ctx context.Context, tx *sql.Tx, sessionID string, file protocol.FileChange, hunk *protocol.ChangeHunk, fileOrder, hunkOrder int, createdAt string) error {
	rowID := file.ID
	var hunkID any
	var oldStart, oldLines, newStart, newLines any
	var originalText, modifiedText any
	var hunkStatus any
	if hunk != nil {
		rowID = hunk.ID
		hunkID = hunk.ID
		oldStart, oldLines = hunk.OldStart, hunk.OldLines
		newStart, newLines = hunk.NewStart, hunk.NewLines
		originalText, modifiedText = hunk.OriginalText, hunk.ModifiedText
		hunkStatus = hunk.Status
	}
	filePath := pointerValue(file.NewPath)
	if filePath == "" {
		filePath = pointerValue(file.OldPath)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO changes(
			id, session_id, file_path, old_path, change_kind, review_mode,
			old_start, old_lines, new_start, new_lines, original_text, modified_text,
			status, created_at, file_id, hunk_id, new_path, original_file,
			modified_file, file_status, hunk_status, file_order, hunk_order
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rowID, sessionID, filePath, nullablePointer(file.OldPath), file.Kind, file.ReviewMode,
		oldStart, oldLines, newStart, newLines, originalText, modifiedText,
		file.Status, createdAt, file.ID, hunkID, nullablePointer(file.NewPath),
		nullablePointer(file.Original), nullablePointer(file.Modified), file.Status, hunkStatus,
		fileOrder, hunkOrder,
	)
	if err != nil {
		return fmt.Errorf("insert change %q: %w", rowID, err)
	}
	return nil
}

func (s *Store) GetChangeSet(ctx context.Context, sessionID string) (protocol.ChangeSet, error) {
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		return protocol.ChangeSet{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT file_id, hunk_id, old_path, new_path, change_kind, review_mode,
		       original_file, modified_file, file_status,
		       old_start, old_lines, new_start, new_lines,
		       original_text, modified_text, hunk_status
		FROM changes
		WHERE session_id = ? AND file_id IS NOT NULL
		ORDER BY file_order, hunk_order`, sessionID)
	if err != nil {
		return protocol.ChangeSet{}, fmt.Errorf("query change set: %w", err)
	}
	defer rows.Close()

	result := protocol.ChangeSet{SessionID: sessionID, Files: []protocol.FileChange{}}
	fileIndexes := make(map[string]int)
	for rows.Next() {
		var fileID string
		var hunkID, oldPath, newPath, originalFile, modifiedFile sql.NullString
		var kind protocol.FileChangeKind
		var reviewMode string
		var fileStatus protocol.ReviewStatus
		var oldStart, oldLines, newStart, newLines sql.NullInt64
		var originalText, modifiedText, hunkStatus sql.NullString
		if err := rows.Scan(
			&fileID, &hunkID, &oldPath, &newPath, &kind, &reviewMode,
			&originalFile, &modifiedFile, &fileStatus,
			&oldStart, &oldLines, &newStart, &newLines,
			&originalText, &modifiedText, &hunkStatus,
		); err != nil {
			return protocol.ChangeSet{}, fmt.Errorf("scan change: %w", err)
		}
		fileIndex, exists := fileIndexes[fileID]
		if !exists {
			fileIndex = len(result.Files)
			fileIndexes[fileID] = fileIndex
			result.Files = append(result.Files, protocol.FileChange{
				ID: fileID, OldPath: nullStringPointer(oldPath), NewPath: nullStringPointer(newPath),
				Kind: kind, Original: nullStringPointer(originalFile), Modified: nullStringPointer(modifiedFile),
				ReviewMode: reviewMode, Status: fileStatus, Hunks: []protocol.ChangeHunk{},
			})
		}
		if hunkID.Valid {
			status := protocol.ReviewPending
			if hunkStatus.Valid {
				status = protocol.ReviewStatus(hunkStatus.String)
			}
			result.Files[fileIndex].Hunks = append(result.Files[fileIndex].Hunks, protocol.ChangeHunk{
				ID: hunkID.String, OldStart: int(oldStart.Int64), OldLines: int(oldLines.Int64),
				NewStart: int(newStart.Int64), NewLines: int(newLines.Int64),
				OriginalText: originalText.String, ModifiedText: modifiedText.String, Status: status,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return protocol.ChangeSet{}, fmt.Errorf("iterate changes: %w", err)
	}
	return result, nil
}

func (s *Store) UpdateHunkReview(ctx context.Context, sessionID, hunkID string, status protocol.ReviewStatus, reviewedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin hunk review: %w", err)
	}
	defer tx.Rollback()
	var fileID string
	var current protocol.ReviewStatus
	if err := tx.QueryRowContext(ctx, `
		SELECT file_id, hunk_status FROM changes
		WHERE session_id = ? AND hunk_id = ?`, sessionID, hunkID,
	).Scan(&fileID, &current); err != nil {
		if err == sql.ErrNoRows {
			return storage.ErrNotFound
		}
		return fmt.Errorf("read hunk review: %w", err)
	}
	if current != protocol.ReviewPending {
		if current == status {
			return nil
		}
		return storage.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE changes SET hunk_status = ?, reviewed_at = ?
		WHERE session_id = ? AND hunk_id = ?`,
		status, formatTime(reviewedAt), sessionID, hunkID,
	); err != nil {
		return fmt.Errorf("update hunk review: %w", err)
	}
	var total, accepted, rejected, pending int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN hunk_status = 'accepted' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN hunk_status = 'rejected' THEN 1 ELSE 0 END),
		       SUM(CASE WHEN hunk_status = 'pending' THEN 1 ELSE 0 END)
		FROM changes WHERE session_id = ? AND file_id = ? AND hunk_id IS NOT NULL`,
		sessionID, fileID,
	).Scan(&total, &accepted, &rejected, &pending); err != nil {
		return fmt.Errorf("summarize hunk review: %w", err)
	}
	fileStatus := protocol.ReviewPending
	switch {
	case accepted == total:
		fileStatus = protocol.ReviewAccepted
	case rejected == total:
		fileStatus = protocol.ReviewRejected
	case accepted > 0:
		fileStatus = protocol.ReviewPartiallyAccepted
	case pending == 0:
		fileStatus = protocol.ReviewRejected
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE changes SET file_status = ?, status = ?
		WHERE session_id = ? AND file_id = ?`,
		fileStatus, fileStatus, sessionID, fileID,
	); err != nil {
		return fmt.Errorf("update file review summary: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit hunk review: %w", err)
	}
	return nil
}

func (s *Store) UpdateFileReview(ctx context.Context, sessionID, fileID string, status protocol.ReviewStatus, reviewedAt time.Time) error {
	var current protocol.ReviewStatus
	err := s.db.QueryRowContext(ctx, `
		SELECT file_status FROM changes
		WHERE session_id = ? AND file_id = ? LIMIT 1`, sessionID, fileID,
	).Scan(&current)
	if err == sql.ErrNoRows {
		return storage.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read file review: %w", err)
	}
	if current != protocol.ReviewPending {
		if current == status {
			return nil
		}
		return storage.ErrConflict
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE changes
		SET file_status = ?, status = ?, reviewed_at = ?
		WHERE session_id = ? AND file_id = ?`,
		status, status, formatTime(reviewedAt), sessionID, fileID,
	)
	return updateResult("update file review", result, err)
}

func nullablePointer(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
