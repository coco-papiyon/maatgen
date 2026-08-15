package review

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

var (
	ErrConflict        = errors.New("working tree changed outside maatgen")
	ErrAlreadyReviewed = errors.New("change was already reviewed differently")
	ErrSessionClosed   = errors.New("session is closed")
	ErrUnsupported     = errors.New("change type is not supported for review")
)

type Store interface {
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
	GetChangeSet(ctx context.Context, sessionID string) (protocol.ChangeSet, error)
	UpdateHunkReview(ctx context.Context, sessionID, hunkID string, status protocol.ReviewStatus, reviewedAt time.Time) error
	UpdateFileReview(ctx context.Context, sessionID, fileID string, status protocol.ReviewStatus, reviewedAt time.Time) error
	AppendEvent(ctx context.Context, event protocol.SessionEvent) (protocol.SessionEvent, error)
}

type SessionCloser interface {
	CloseSession(ctx context.Context, id string) (protocol.AgentSession, error)
}

type Service struct {
	store  Store
	closer SessionCloser
	mu     sync.Mutex
	now    func() time.Time
}

type Option func(*Service)

func WithSessionCloser(closer SessionCloser) Option {
	return func(service *Service) { service.closer = closer }
}

func New(store Store, options ...Option) *Service {
	service := &Service{store: store, now: time.Now}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) Accept(ctx context.Context, sessionID, changeID string) (protocol.ChangeSet, error) {
	return s.review(ctx, sessionID, changeID, protocol.ReviewAccepted)
}

func (s *Service) Reject(ctx context.Context, sessionID, changeID string) (protocol.ChangeSet, error) {
	return s.review(ctx, sessionID, changeID, protocol.ReviewRejected)
}

func (s *Service) AcceptAll(ctx context.Context, sessionID string) (protocol.ChangeSet, error) {
	return s.reviewAll(ctx, sessionID, protocol.ReviewAccepted)
}

func (s *Service) RejectAll(ctx context.Context, sessionID string) (protocol.ChangeSet, error) {
	return s.reviewAll(ctx, sessionID, protocol.ReviewRejected)
}

func (s *Service) review(ctx context.Context, sessionID, changeID string, decision protocol.ReviewStatus) (protocol.ChangeSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	changeSet, err := s.store.GetChangeSet(ctx, sessionID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	file, hunk := findChange(changeSet, changeID)
	if file == nil {
		return protocol.ChangeSet{}, storage.ErrNotFound
	}
	current := file.Status
	if hunk != nil {
		current = hunk.Status
	}
	if session.Status != protocol.SessionActive {
		if current == decision {
			return s.retryCleanup(ctx, session, changeSet)
		}
		return protocol.ChangeSet{}, ErrSessionClosed
	}
	if current != protocol.ReviewPending {
		if current == decision {
			return s.finalize(ctx, sessionID, changeSet)
		}
		return protocol.ChangeSet{}, ErrAlreadyReviewed
	}
	if decision == protocol.ReviewAccepted {
		if hunk != nil {
			err = applyHunk(session.Workspace, *file, hunk.ID)
		} else {
			err = applyFile(session, *file)
		}
		if err != nil {
			return protocol.ChangeSet{}, err
		}
	}
	reviewedAt := s.now().UTC()
	if hunk != nil {
		err = s.store.UpdateHunkReview(ctx, sessionID, hunk.ID, decision, reviewedAt)
	} else {
		err = s.store.UpdateFileReview(ctx, sessionID, file.ID, decision, reviewedAt)
	}
	if errors.Is(err, storage.ErrConflict) {
		return protocol.ChangeSet{}, ErrAlreadyReviewed
	}
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	if err := s.appendEvent(ctx, sessionID, changeID, decision, hunk != nil); err != nil {
		return protocol.ChangeSet{}, err
	}
	updated, err := s.store.GetChangeSet(ctx, sessionID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	return s.finalize(ctx, sessionID, updated)
}

func (s *Service) reviewAll(ctx context.Context, sessionID string, decision protocol.ReviewStatus) (protocol.ChangeSet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	changeSet, err := s.store.GetChangeSet(ctx, sessionID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	if session.Status != protocol.SessionActive {
		if allReviewed(changeSet) {
			return s.retryCleanup(ctx, session, changeSet)
		}
		return protocol.ChangeSet{}, ErrSessionClosed
	}
	for {
		file, hunk := firstPending(changeSet)
		if file == nil {
			break
		}
		changeID := file.ID
		if hunk != nil {
			changeID = hunk.ID
		}
		if decision == protocol.ReviewAccepted {
			if hunk != nil {
				err = applyHunk(session.Workspace, *file, hunk.ID)
			} else {
				err = applyFile(session, *file)
			}
			if err != nil {
				return changeSet, err
			}
		}
		if hunk != nil {
			err = s.store.UpdateHunkReview(ctx, sessionID, hunk.ID, decision, s.now().UTC())
		} else {
			err = s.store.UpdateFileReview(ctx, sessionID, file.ID, decision, s.now().UTC())
		}
		if err != nil {
			return changeSet, err
		}
		if err := s.appendEvent(ctx, sessionID, changeID, decision, hunk != nil); err != nil {
			return changeSet, err
		}
		changeSet, err = s.store.GetChangeSet(ctx, sessionID)
		if err != nil {
			return protocol.ChangeSet{}, err
		}
	}
	return s.finalize(ctx, sessionID, changeSet)
}

func firstPending(changeSet protocol.ChangeSet) (*protocol.FileChange, *protocol.ChangeHunk) {
	for fileIndex := range changeSet.Files {
		file := &changeSet.Files[fileIndex]
		if file.ReviewMode == "file" && file.Status == protocol.ReviewPending {
			return file, nil
		}
		for hunkIndex := range file.Hunks {
			if file.Hunks[hunkIndex].Status == protocol.ReviewPending {
				return file, &file.Hunks[hunkIndex]
			}
		}
	}
	return nil, nil
}

func (s *Service) finalize(ctx context.Context, sessionID string, changeSet protocol.ChangeSet) (protocol.ChangeSet, error) {
	if s.closer == nil || !allReviewed(changeSet) {
		return changeSet, nil
	}
	if _, err := s.closer.CloseSession(ctx, sessionID); err != nil {
		return changeSet, err
	}
	return changeSet, nil
}

func (s *Service) retryCleanup(ctx context.Context, session protocol.AgentSession, changeSet protocol.ChangeSet) (protocol.ChangeSet, error) {
	if s.closer != nil && session.CleanupStatus != protocol.CleanupCompleted {
		if _, err := s.closer.CloseSession(ctx, session.ID); err != nil {
			return changeSet, err
		}
	}
	return changeSet, nil
}

func allReviewed(changeSet protocol.ChangeSet) bool {
	for _, file := range changeSet.Files {
		if file.Status == protocol.ReviewPending {
			return false
		}
		for _, hunk := range file.Hunks {
			if hunk.Status == protocol.ReviewPending {
				return false
			}
		}
	}
	return true
}

func findChange(changeSet protocol.ChangeSet, id string) (*protocol.FileChange, *protocol.ChangeHunk) {
	for fileIndex := range changeSet.Files {
		file := &changeSet.Files[fileIndex]
		if file.ID == id && file.ReviewMode == "file" {
			return file, nil
		}
		for hunkIndex := range file.Hunks {
			if file.Hunks[hunkIndex].ID == id {
				return file, &file.Hunks[hunkIndex]
			}
		}
	}
	return nil, nil
}

func applyHunk(workspace string, file protocol.FileChange, targetID string) error {
	if file.Original == nil {
		empty := ""
		file.Original = &empty
	}
	before := acceptedContent(*file.Original, file.Hunks, "")
	after := acceptedContent(*file.Original, file.Hunks, targetID)
	path := pointerValue(file.NewPath)
	if path == "" {
		path = pointerValue(file.OldPath)
	}
	target, err := safePath(workspace, path)
	if err != nil {
		return err
	}
	expectedExists := file.Kind != protocol.FileAdd || hasAcceptedHunk(file.Hunks, "")
	if err := verifyFile(target, []byte(before), expectedExists); err != nil {
		return err
	}
	allAccepted := true
	for _, hunk := range file.Hunks {
		if hunk.Status != protocol.ReviewAccepted && hunk.ID != targetID {
			allAccepted = false
			break
		}
	}
	if file.Kind == protocol.FileDelete && allAccepted {
		return removeFile(target)
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect reviewed file: %w", err)
	}
	return atomicWrite(target, []byte(after), mode)
}

func acceptedContent(original string, hunks []protocol.ChangeHunk, includeID string) string {
	content := []byte(original)
	selected := make([]protocol.ChangeHunk, 0, len(hunks))
	for _, hunk := range hunks {
		if hunk.Status == protocol.ReviewAccepted || hunk.ID == includeID {
			selected = append(selected, hunk)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].OldStart > selected[j].OldStart })
	for _, hunk := range selected {
		start := lineOffset(content, hunk.OldStart)
		end := lineOffset(content, hunk.OldStart+hunk.OldLines)
		if hunk.OldStart == 0 {
			start, end = 0, 0
		}
		content = append(append(append([]byte{}, content[:start]...), []byte(hunk.ModifiedText)...), content[end:]...)
	}
	return string(content)
}

func lineOffset(content []byte, oneBasedLine int) int {
	if oneBasedLine <= 1 {
		return 0
	}
	line := 1
	for index, value := range content {
		if value == '\n' {
			line++
			if line == oneBasedLine {
				return index + 1
			}
		}
	}
	return len(content)
}

func hasAcceptedHunk(hunks []protocol.ChangeHunk, includeID string) bool {
	for _, hunk := range hunks {
		if hunk.Status == protocol.ReviewAccepted || hunk.ID == includeID {
			return true
		}
	}
	return false
}

func applyFile(session protocol.AgentSession, file protocol.FileChange) error {
	oldPath, newPath := pointerValue(file.OldPath), pointerValue(file.NewPath)
	oldTarget, err := optionalSafePath(session.Workspace, oldPath)
	if err != nil {
		return err
	}
	newTarget, err := optionalSafePath(session.Workspace, newPath)
	if err != nil {
		return err
	}
	var expected []byte
	expectedExists := oldPath != ""
	if file.Original != nil {
		expected = []byte(*file.Original)
	} else if expectedExists {
		expected, err = readBaseFile(session, oldPath)
		if err != nil {
			return fmt.Errorf("read base file: %w", err)
		}
	}
	if expectedExists {
		if err := verifyFile(oldTarget, expected, true); err != nil {
			return err
		}
	} else if err := verifyFile(newTarget, nil, false); err != nil {
		return err
	}
	if newPath == "" {
		return removeFile(oldTarget)
	}
	worktreeTarget, err := safePath(session.Worktree, newPath)
	if err != nil {
		return err
	}
	desired, err := readFile(worktreeTarget)
	if err != nil {
		return fmt.Errorf("read agent file: %w", err)
	}
	info, err := os.Stat(worktreeTarget)
	if err != nil {
		return fmt.Errorf("inspect agent file: %w", err)
	}
	if oldPath != "" && oldPath != newPath {
		if err := verifyFile(newTarget, nil, false); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(newTarget), 0o755); err != nil {
			return err
		}
		if err := os.Rename(oldTarget, newTarget); err != nil {
			return fmt.Errorf("rename reviewed file: %w", err)
		}
	}
	return atomicWrite(newTarget, desired, info.Mode().Perm())
}

func verifyFile(path string, expected []byte, expectedExists bool) error {
	actual, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if expectedExists {
			return ErrConflict
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read working tree file: %w", err)
	}
	if !expectedExists || !bytes.Equal(actual, expected) {
		return ErrConflict
	}
	return nil
}

func readBaseFile(session protocol.AgentSession, path string) ([]byte, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	command := exec.Command(gitPath, "-C", session.Workspace, "show", session.BaseCommit+":"+path)
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	return output, nil
}

func readFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsupported
	}
	return os.ReadFile(path)
}

func atomicWrite(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".maatgen-review-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set file mode: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace reviewed file: %w", err)
	}
	return nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove reviewed file: %w", err)
	}
	return nil
}

func safePath(root, relative string) (string, error) {
	if strings.TrimSpace(relative) == "" {
		return "", errors.New("change path is empty")
	}
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(rootPath, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	if target != rootPath && !strings.HasPrefix(target, rootPath+string(filepath.Separator)) {
		return "", errors.New("change path escapes workspace")
	}
	return target, nil
}

func optionalSafePath(root, relative string) (string, error) {
	if relative == "" {
		return "", nil
	}
	return safePath(root, relative)
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) appendEvent(ctx context.Context, sessionID, changeID string, decision protocol.ReviewStatus, hunk bool) error {
	id, err := newID("event")
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{"changeId": changeID, "decision": decision, "scope": map[bool]string{true: "hunk", false: "file"}[hunk]})
	_, err = s.store.AppendEvent(ctx, protocol.SessionEvent{
		ID: id, SessionID: sessionID, Timestamp: s.now().UTC(), SchemaVersion: protocol.SchemaVersion,
		Source: protocol.EventSourceUser, Type: protocol.EventTypeChangeReviewed, Data: data,
	})
	return err
}

func newID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}
