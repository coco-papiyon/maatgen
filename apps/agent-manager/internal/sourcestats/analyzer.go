// Package sourcestats counts source lines per language with cloc, once per
// Session at creation time.
package sourcestats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

const totalLanguageKey = "SUM"

var ErrUnavailable = errors.New("cloc is not available")

// Analyzer runs cloc against a repository and parses its JSON report.
type Analyzer struct {
	binaryName string
	prefixArgs []string
	timeout    time.Duration
}

func New(binaryName string) *Analyzer {
	if strings.TrimSpace(binaryName) == "" {
		binaryName = "cloc"
	}
	return &Analyzer{binaryName: binaryName, timeout: 2 * time.Minute}
}

type clocEntry struct {
	Files   int `json:"nFiles"`
	Blank   int `json:"blank"`
	Comment int `json:"comment"`
	Code    int `json:"code"`
}

// Analyze counts git-tracked source files under repository by language.
func (a *Analyzer) Analyze(ctx context.Context, repository string) (protocol.SourceStats, error) {
	if _, err := exec.LookPath(a.binaryName); err != nil {
		return protocol.SourceStats{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	runCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	// cloc's bundled Perl mishandles non-ASCII Windows paths (e.g. a Japanese
	// folder name), failing to chdir back after scanning subdirectories. The
	// short path form is pure ASCII and sidesteps that bug when available.
	target := toShortPath(repository)
	args := append(append([]string{}, a.prefixArgs...), "--vcs=git", "--json", target)
	command := exec.CommandContext(runCtx, a.binaryName, args...)
	command.Dir = target
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil && stdout.Len() == 0 {
		return protocol.SourceStats{}, fmt.Errorf("run cloc: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return protocol.SourceStats{}, fmt.Errorf("parse cloc output: %w", err)
	}
	result := protocol.SourceStats{Languages: []protocol.SourceStatsLanguage{}}
	for language, value := range raw {
		if language == "header" {
			continue
		}
		var entry clocEntry
		if err := json.Unmarshal(value, &entry); err != nil {
			continue
		}
		stats := protocol.SourceStatsLanguage{Language: language, Files: entry.Files, Blank: entry.Blank, Comment: entry.Comment, Code: entry.Code}
		if language == totalLanguageKey {
			stats.Language = ""
			result.Total = stats
			continue
		}
		result.Languages = append(result.Languages, stats)
	}
	sort.Slice(result.Languages, func(i, j int) bool { return result.Languages[i].Code > result.Languages[j].Code })
	return result, nil
}
