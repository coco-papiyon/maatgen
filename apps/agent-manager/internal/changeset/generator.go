package changeset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type Generator struct{ gitPath string }
type detectedFile struct{ status, oldPath, newPath string }

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func New() (*Generator, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("find git executable: %w", err)
	}
	return &Generator{gitPath: gitPath}, nil
}

func (g *Generator) Generate(ctx context.Context, repository string, checkpoint protocol.Checkpoint) (protocol.ChangeSet, error) {
	if checkpoint.AfterTree == nil {
		return protocol.ChangeSet{}, errors.New("checkpoint after tree is required")
	}
	files, err := g.changedFiles(ctx, repository, checkpoint.BeforeTree, *checkpoint.AfterTree)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	result := protocol.ChangeSet{SessionID: checkpoint.SessionID, RunID: checkpoint.RunID, CheckpointID: checkpoint.ID,
		BeforeTree: checkpoint.BeforeTree, AfterTree: *checkpoint.AfterTree, Files: make([]protocol.FileChange, 0, len(files))}
	for _, detected := range files {
		change, err := g.buildFileChange(ctx, repository, checkpoint.BeforeTree, *checkpoint.AfterTree, detected)
		if err != nil {
			return protocol.ChangeSet{}, err
		}
		result.Files = append(result.Files, change)
	}
	return result, nil
}

func (g *Generator) changedFiles(ctx context.Context, repository, before, after string) ([]detectedFile, error) {
	output, err := g.run(ctx, repository, "diff", "--name-status", "-z", "--find-renames", before, after, "--")
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}
	tokens := splitNUL(output)
	result := make([]detectedFile, 0, len(tokens)/2)
	for index := 0; index < len(tokens); {
		status := tokens[index]
		index++
		if status == "" || index >= len(tokens) {
			return nil, errors.New("parse Git name-status output")
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if index+1 >= len(tokens) {
				return nil, errors.New("parse Git rename output")
			}
			result = append(result, detectedFile{status: "R", oldPath: tokens[index], newPath: tokens[index+1]})
			index += 2
			continue
		}
		path := tokens[index]
		index++
		switch status[:1] {
		case "A":
			result = append(result, detectedFile{status: "A", newPath: path})
		case "D":
			result = append(result, detectedFile{status: "D", oldPath: path})
		default:
			result = append(result, detectedFile{status: "M", oldPath: path, newPath: path})
		}
	}
	return result, nil
}

func (g *Generator) buildFileChange(ctx context.Context, repository, before, after string, file detectedFile) (protocol.FileChange, error) {
	var originalBytes, modifiedBytes []byte
	var err error
	if file.oldPath != "" {
		originalBytes, err = g.readTree(ctx, repository, before, file.oldPath)
		if err != nil {
			return protocol.FileChange{}, fmt.Errorf("read before file %q: %w", file.oldPath, err)
		}
	}
	if file.newPath != "" {
		modifiedBytes, err = g.readTree(ctx, repository, after, file.newPath)
		if err != nil {
			return protocol.FileChange{}, fmt.Errorf("read after file %q: %w", file.newPath, err)
		}
	}
	kind := protocol.FileModify
	restoreMode := "hunk"
	switch file.status {
	case "A":
		kind = protocol.FileAdd
	case "D":
		kind = protocol.FileDelete
	case "R":
		kind = protocol.FileRename
		restoreMode = "file"
	}
	if bytes.IndexByte(originalBytes, 0) >= 0 || bytes.IndexByte(modifiedBytes, 0) >= 0 {
		kind = protocol.FileBinary
		restoreMode = "file"
	} else if file.status == "M" && bytes.Equal(originalBytes, modifiedBytes) {
		kind = protocol.FileModeChange
		restoreMode = "file"
	}
	change := protocol.FileChange{OldPath: stringPointer(file.oldPath), NewPath: stringPointer(file.newPath), Kind: kind, RestoreMode: restoreMode, Status: protocol.RestoreChanged, Hunks: []protocol.ChangeHunk{}}
	if kind != protocol.FileBinary {
		if file.oldPath != "" {
			v := string(originalBytes)
			change.Original = &v
		}
		if file.newPath != "" {
			v := string(modifiedBytes)
			change.Modified = &v
		}
	}
	change.ID = stableID("file", file.oldPath, file.newPath, string(kind), string(originalBytes), string(modifiedBytes))
	if restoreMode == "hunk" {
		path := firstNonEmpty(file.newPath, file.oldPath)
		patch, err := g.run(ctx, repository, "diff", "--no-color", "--no-ext-diff", "--unified=3", before, after, "--", path)
		if err != nil {
			return protocol.FileChange{}, err
		}
		change.Hunks, err = parseHunks(patch, file.oldPath, file.newPath)
		if err != nil {
			return protocol.FileChange{}, err
		}
		if file.status == "A" && len(change.Hunks) == 0 && len(modifiedBytes) > 0 {
			change.Hunks = []protocol.ChangeHunk{wholeFileHunk(file.oldPath, file.newPath, nil, modifiedBytes)}
		}
		if len(change.Hunks) == 0 {
			change.RestoreMode = "file"
		}
	}
	return change, nil
}

func parseHunks(patch, oldPath, newPath string) ([]protocol.ChangeHunk, error) {
	lines := strings.SplitAfter(patch, "\n")
	var result []protocol.ChangeHunk
	for index := 0; index < len(lines); {
		line := strings.TrimSuffix(lines[index], "\n")
		matches := hunkHeader.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if matches == nil {
			index++
			continue
		}
		oldStart, _ := strconv.Atoi(matches[1])
		oldLines := parseCount(matches[2])
		newStart, _ := strconv.Atoi(matches[3])
		newLines := parseCount(matches[4])
		index++
		var original, modified strings.Builder
		lastPrefix := byte(0)
		for index < len(lines) {
			current := lines[index]
			trimmed := strings.TrimSuffix(strings.TrimSuffix(current, "\n"), "\r")
			if hunkHeader.MatchString(trimmed) || strings.HasPrefix(trimmed, "diff --git ") {
				break
			}
			if strings.HasPrefix(trimmed, `\ No newline at end of file`) {
				if lastPrefix == '-' || lastPrefix == ' ' {
					trimTrailingNewline(&original)
				}
				if lastPrefix == '+' || lastPrefix == ' ' {
					trimTrailingNewline(&modified)
				}
				index++
				continue
			}
			if len(current) > 0 {
				lastPrefix = current[0]
				content := current[1:]
				switch lastPrefix {
				case ' ':
					original.WriteString(content)
					modified.WriteString(content)
				case '-':
					original.WriteString(content)
				case '+':
					modified.WriteString(content)
				}
			}
			index++
		}
		h := protocol.ChangeHunk{OldStart: oldStart, OldLines: oldLines, NewStart: newStart, NewLines: newLines, OriginalText: original.String(), ModifiedText: modified.String(), Status: protocol.RestoreChanged}
		h.ID = stableID("hunk", oldPath, newPath, strconv.Itoa(oldStart), strconv.Itoa(oldLines), strconv.Itoa(newStart), strconv.Itoa(newLines), h.OriginalText, h.ModifiedText)
		result = append(result, h)
	}
	return result, nil
}

func wholeFileHunk(oldPath, newPath string, original, modified []byte) protocol.ChangeHunk {
	h := protocol.ChangeHunk{OldStart: 0, OldLines: lineCount(original), NewStart: 1, NewLines: lineCount(modified), OriginalText: string(original), ModifiedText: string(modified), Status: protocol.RestoreChanged}
	h.ID = stableID("hunk", oldPath, newPath, "0", strconv.Itoa(h.OldLines), "1", strconv.Itoa(h.NewLines), h.OriginalText, h.ModifiedText)
	return h
}
func (g *Generator) readTree(ctx context.Context, repository, tree, path string) ([]byte, error) {
	return g.runBytes(ctx, repository, "show", tree+":"+path)
}
func (g *Generator) run(ctx context.Context, directory string, args ...string) (string, error) {
	b, e := g.runBytes(ctx, directory, args...)
	return string(b), e
}
func (g *Generator) runBytes(ctx context.Context, directory string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, g.gitPath, append([]string{"-C", directory}, args...)...)
	var out, stderr bytes.Buffer
	c.Stdout = &out
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		m := strings.TrimSpace(stderr.String())
		if m == "" {
			m = err.Error()
		}
		return nil, errors.New(m)
	}
	return out.Bytes(), nil
}
func splitNUL(v string) []string {
	p := strings.Split(v, "\x00")
	if len(p) > 0 && p[len(p)-1] == "" {
		p = p[:len(p)-1]
	}
	return p
}
func parseCount(v string) int {
	if v == "" {
		return 1
	}
	n, _ := strconv.Atoi(v)
	return n
}
func trimTrailingNewline(b *strings.Builder) {
	v := strings.TrimSuffix(strings.TrimSuffix(b.String(), "\n"), "\r")
	b.Reset()
	b.WriteString(v)
}
func stableID(prefix string, values ...string) string {
	h := sha256.New()
	for _, v := range values {
		_, _ = h.Write([]byte(strconv.Itoa(len(v))))
		_, _ = h.Write([]byte{':'})
		_, _ = h.Write([]byte(v))
	}
	return prefix + "_" + hex.EncodeToString(h.Sum(nil))
}
func stringPointer(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func lineCount(v []byte) int {
	if len(v) == 0 {
		return 0
	}
	return bytes.Count(v, []byte{'\n'}) + 1 - boolInt(v[len(v)-1] == '\n')
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
