package changeset

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

type Generator struct {
	gitPath string
}

type detectedFile struct {
	status  string
	oldPath string
	newPath string
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func New() (*Generator, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("find git executable: %w", err)
	}
	return &Generator{gitPath: gitPath}, nil
}

func (g *Generator) Generate(ctx context.Context, session protocol.AgentSession) (protocol.ChangeSet, error) {
	if strings.TrimSpace(session.Worktree) == "" || strings.TrimSpace(session.BaseCommit) == "" {
		return protocol.ChangeSet{}, errors.New("worktree and base commit are required")
	}
	tracked, err := g.changedFiles(ctx, session.Worktree, session.BaseCommit)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	untracked, err := g.untrackedFiles(ctx, session.Worktree)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	files := make([]detectedFile, 0, len(tracked)+len(untracked))
	files = append(files, tracked...)
	for _, path := range untracked {
		files = append(files, detectedFile{status: "A", newPath: path})
	}
	files = g.detectExactRenames(ctx, session, files)

	result := protocol.ChangeSet{SessionID: session.ID, Files: make([]protocol.FileChange, 0, len(files))}
	for _, detected := range files {
		change, err := g.buildFileChange(ctx, session, detected)
		if err != nil {
			return protocol.ChangeSet{}, err
		}
		result.Files = append(result.Files, change)
	}
	return result, nil
}

func (g *Generator) changedFiles(ctx context.Context, worktree, base string) ([]detectedFile, error) {
	output, err := g.run(ctx, worktree, "diff", "--name-status", "-z", "--find-renames", base, "--")
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

func (g *Generator) untrackedFiles(ctx context.Context, worktree string) ([]string, error) {
	output, err := g.run(ctx, worktree, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w", err)
	}
	return splitNUL(output), nil
}

func (g *Generator) detectExactRenames(ctx context.Context, session protocol.AgentSession, files []detectedFile) []detectedFile {
	deletedByHash := make(map[string][]int)
	addedByHash := make(map[string][]int)
	for index, file := range files {
		var content []byte
		var err error
		switch file.status {
		case "D":
			content, err = g.readBase(ctx, session.Worktree, session.BaseCommit, file.oldPath)
		case "A":
			content, err = readWorktreeFile(session.Worktree, file.newPath)
		default:
			continue
		}
		if err != nil {
			continue
		}
		hash := sha256.Sum256(content)
		key := hex.EncodeToString(hash[:])
		if file.status == "D" {
			deletedByHash[key] = append(deletedByHash[key], index)
		} else {
			addedByHash[key] = append(addedByHash[key], index)
		}
	}
	removed := make(map[int]bool)
	for hash, deleted := range deletedByHash {
		added := addedByHash[hash]
		for pair := 0; pair < len(deleted) && pair < len(added); pair++ {
			files[deleted[pair]] = detectedFile{
				status: "R", oldPath: files[deleted[pair]].oldPath, newPath: files[added[pair]].newPath,
			}
			removed[added[pair]] = true
		}
	}
	result := make([]detectedFile, 0, len(files)-len(removed))
	for index, file := range files {
		if !removed[index] {
			result = append(result, file)
		}
	}
	return result
}

func (g *Generator) buildFileChange(ctx context.Context, session protocol.AgentSession, file detectedFile) (protocol.FileChange, error) {
	var originalBytes, modifiedBytes []byte
	var err error
	if file.oldPath != "" {
		originalBytes, err = g.readBase(ctx, session.Worktree, session.BaseCommit, file.oldPath)
		if err != nil {
			return protocol.FileChange{}, fmt.Errorf("read base file %q: %w", file.oldPath, err)
		}
	}
	if file.newPath != "" {
		modifiedBytes, err = readWorktreeFile(session.Worktree, file.newPath)
		if err != nil {
			return protocol.FileChange{}, fmt.Errorf("read changed file %q: %w", file.newPath, err)
		}
	}

	kind := protocol.FileModify
	reviewMode := "hunk"
	switch file.status {
	case "A":
		kind = protocol.FileAdd
	case "D":
		kind = protocol.FileDelete
	case "R":
		kind = protocol.FileRename
		reviewMode = "file"
	}
	if bytes.IndexByte(originalBytes, 0) >= 0 || bytes.IndexByte(modifiedBytes, 0) >= 0 {
		kind = protocol.FileBinary
		reviewMode = "file"
	} else if file.status == "M" && bytes.Equal(originalBytes, modifiedBytes) {
		kind = protocol.FileModeChange
		reviewMode = "file"
	}

	change := protocol.FileChange{
		OldPath: stringPointer(file.oldPath), NewPath: stringPointer(file.newPath), Kind: kind,
		ReviewMode: reviewMode, Status: protocol.ReviewPending, Hunks: []protocol.ChangeHunk{},
	}
	if kind != protocol.FileBinary {
		if file.oldPath != "" {
			value := string(originalBytes)
			change.Original = &value
		}
		if file.newPath != "" {
			value := string(modifiedBytes)
			change.Modified = &value
		}
	}
	change.ID = stableID("file", file.oldPath, file.newPath, string(kind), string(originalBytes), string(modifiedBytes))

	if reviewMode == "hunk" {
		patch, err := g.run(ctx, session.Worktree, "diff", "--no-color", "--no-ext-diff", "--unified=3", session.BaseCommit, "--", firstNonEmpty(file.newPath, file.oldPath))
		if err != nil {
			return protocol.FileChange{}, fmt.Errorf("generate diff for %q: %w", firstNonEmpty(file.newPath, file.oldPath), err)
		}
		change.Hunks, err = parseHunks(patch, file.oldPath, file.newPath)
		if err != nil {
			return protocol.FileChange{}, fmt.Errorf("parse diff for %q: %w", firstNonEmpty(file.newPath, file.oldPath), err)
		}
		if file.status == "A" && len(change.Hunks) == 0 && len(modifiedBytes) > 0 {
			change.Hunks = []protocol.ChangeHunk{wholeFileHunk(file.oldPath, file.newPath, nil, modifiedBytes)}
		}
		if len(change.Hunks) == 0 {
			change.ReviewMode = "file"
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
		hunk := protocol.ChangeHunk{
			OldStart: oldStart, OldLines: oldLines, NewStart: newStart, NewLines: newLines,
			OriginalText: original.String(), ModifiedText: modified.String(), Status: protocol.ReviewPending,
		}
		hunk.ID = stableID("hunk", oldPath, newPath, strconv.Itoa(oldStart), strconv.Itoa(oldLines), strconv.Itoa(newStart), strconv.Itoa(newLines), hunk.OriginalText, hunk.ModifiedText)
		result = append(result, hunk)
	}
	return result, nil
}

func wholeFileHunk(oldPath, newPath string, original, modified []byte) protocol.ChangeHunk {
	hunk := protocol.ChangeHunk{
		OldStart: 0, OldLines: lineCount(original), NewStart: 1, NewLines: lineCount(modified),
		OriginalText: string(original), ModifiedText: string(modified), Status: protocol.ReviewPending,
	}
	hunk.ID = stableID("hunk", oldPath, newPath, "0", strconv.Itoa(hunk.OldLines), "1", strconv.Itoa(hunk.NewLines), hunk.OriginalText, hunk.ModifiedText)
	return hunk
}

func (g *Generator) readBase(ctx context.Context, worktree, base, path string) ([]byte, error) {
	return g.runBytes(ctx, worktree, "show", base+":"+path)
}

func readWorktreeFile(worktree, path string) ([]byte, error) {
	root, err := filepath.Abs(worktree)
	if err != nil {
		return nil, err
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return nil, err
	}
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return nil, errors.New("changed path escapes worktree")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(target)
		if err != nil {
			return nil, err
		}
		return []byte(filepath.ToSlash(link)), nil
	}
	return os.ReadFile(target)
}

func (g *Generator) run(ctx context.Context, directory string, args ...string) (string, error) {
	output, err := g.runBytes(ctx, directory, args...)
	return string(output), err
}

func (g *Generator) runBytes(ctx context.Context, directory string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, g.gitPath, append([]string{"-C", directory}, args...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}

func splitNUL(value string) []string {
	parts := strings.Split(value, "\x00")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func parseCount(value string) int {
	if value == "" {
		return 1
	}
	count, _ := strconv.Atoi(value)
	return count
}

func trimTrailingNewline(builder *strings.Builder) {
	value := builder.String()
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	builder.Reset()
	builder.WriteString(value)
}

func stableID(prefix string, values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(value))
	}
	return prefix + "_" + hex.EncodeToString(hash.Sum(nil))
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func lineCount(value []byte) int {
	if len(value) == 0 {
		return 0
	}
	return bytes.Count(value, []byte{'\n'}) + 1 - boolInt(value[len(value)-1] == '\n')
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
