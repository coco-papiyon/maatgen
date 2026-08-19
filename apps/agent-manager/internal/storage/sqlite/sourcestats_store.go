package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// totalLanguageKey stores SourceStats.Total alongside the per-language rows,
// mirroring cloc's own "SUM" entry so the table needs no extra columns.
const totalLanguageKey = "SUM"

func (s *Store) ReplaceSourceStats(ctx context.Context, stats protocol.SourceStats) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace source stats: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM session_source_stats WHERE session_id = ?", stats.SessionID); err != nil {
		return fmt.Errorf("clear source stats: %w", err)
	}
	createdAt := formatTime(time.Now().UTC())
	insert := func(entry protocol.SourceStatsLanguage) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO session_source_stats(session_id, language, files, blank, comment, code, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, stats.SessionID, entry.Language, entry.Files, entry.Blank, entry.Comment, entry.Code, createdAt)
		return err
	}
	for _, language := range stats.Languages {
		if err := insert(language); err != nil {
			return fmt.Errorf("insert source stats language: %w", err)
		}
	}
	total := stats.Total
	total.Language = totalLanguageKey
	if err := insert(total); err != nil {
		return fmt.Errorf("insert source stats total: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit source stats: %w", err)
	}
	return nil
}

func (s *Store) GetSourceStats(ctx context.Context, sessionID string) (protocol.SourceStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT language, files, blank, comment, code FROM session_source_stats
		WHERE session_id = ? ORDER BY code DESC`, sessionID)
	if err != nil {
		return protocol.SourceStats{}, fmt.Errorf("query source stats: %w", err)
	}
	defer rows.Close()
	result := protocol.SourceStats{SessionID: sessionID, Languages: []protocol.SourceStatsLanguage{}}
	for rows.Next() {
		var entry protocol.SourceStatsLanguage
		if err := rows.Scan(&entry.Language, &entry.Files, &entry.Blank, &entry.Comment, &entry.Code); err != nil {
			return protocol.SourceStats{}, fmt.Errorf("scan source stats: %w", err)
		}
		if entry.Language == totalLanguageKey {
			entry.Language = ""
			result.Total = entry
			continue
		}
		result.Languages = append(result.Languages, entry)
	}
	return result, rows.Err()
}
