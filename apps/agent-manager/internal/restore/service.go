package restore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

var (
	ErrConflict      = errors.New("checkpoint restore conflicts with current content")
	ErrSessionClosed = errors.New("session is closed")
	ErrNotRestorable = errors.New("change is not restorable")
)

type Store interface {
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
	GetChangeSetForCheckpoint(ctx context.Context, sessionID, checkpointID string) (protocol.ChangeSet, error)
	UpdateHunkRestore(ctx context.Context, checkpointID, hunkID string, status protocol.RestoreStatus, at time.Time) error
	UpdateFileRestore(ctx context.Context, checkpointID, fileID string, status protocol.RestoreStatus, at time.Time) error
	AppendEvent(ctx context.Context, event protocol.SessionEvent) (protocol.SessionEvent, error)
}

type Snapshotter interface {
	CaptureTree(ctx context.Context, repository string) (string, error)
}

type Service struct {
	store     Store
	snapshots Snapshotter
	gitPath   string
	now       func() time.Time
}

func New(store Store, snapshots Snapshotter) (*Service, error) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	return &Service{store: store, snapshots: snapshots, gitPath: gitPath, now: time.Now}, nil
}

func (s *Service) RestoreHunk(ctx context.Context, sessionID, checkpointID, hunkID string) (protocol.ChangeSet, error) {
	session, set, err := s.load(ctx, sessionID, checkpointID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	file, hunk := findHunk(set, hunkID)
	if file == nil || hunk == nil {
		return protocol.ChangeSet{}, storage.ErrNotFound
	}
	if hunk.Status == protocol.RestoreRestored {
		return set, nil
	}
	if file.RestoreMode == "file" || file.Kind == protocol.FileAdd || file.Kind == protocol.FileDelete {
		return s.RestoreFile(ctx, sessionID, checkpointID, file.ID)
	}
	path := pointerValue(file.NewPath)
	if path == "" {
		path = pointerValue(file.OldPath)
	}
	target, err := safePath(session.Workspace, path)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return protocol.ChangeSet{}, s.conflictHunk(ctx, set, hunk.ID)
	}
	modified := []byte(hunk.ModifiedText)
	if len(modified) == 0 || bytes.Count(content, modified) != 1 {
		return protocol.ChangeSet{}, s.conflictHunk(ctx, set, hunk.ID)
	}
	restored := bytes.Replace(content, modified, []byte(hunk.OriginalText), 1)
	if err := atomicWrite(target, restored, 0); err != nil {
		return protocol.ChangeSet{}, err
	}
	if err := s.store.UpdateHunkRestore(ctx, checkpointID, hunk.ID, protocol.RestoreRestored, s.now().UTC()); err != nil {
		return protocol.ChangeSet{}, err
	}
	_ = s.appendEvent(ctx, set, "restore_hunk", file.ID, hunk.ID)
	return s.store.GetChangeSetForCheckpoint(ctx, sessionID, checkpointID)
}

func (s *Service) RestoreFile(ctx context.Context, sessionID, checkpointID, fileID string) (protocol.ChangeSet, error) {
	session, set, err := s.load(ctx, sessionID, checkpointID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	file := findFile(set, fileID)
	if file == nil {
		return protocol.ChangeSet{}, storage.ErrNotFound
	}
	if file.Status == protocol.RestoreRestored {
		return set, nil
	}
	if err := s.verifyFile(ctx, session.Workspace, set, *file); err != nil {
		_ = s.store.UpdateFileRestore(ctx, checkpointID, file.ID, protocol.RestoreConflict, s.now().UTC())
		return protocol.ChangeSet{}, err
	}
	if err := s.applyBefore(ctx, session.Workspace, set.BeforeTree, *file); err != nil {
		return protocol.ChangeSet{}, err
	}
	if err := s.store.UpdateFileRestore(ctx, checkpointID, file.ID, protocol.RestoreRestored, s.now().UTC()); err != nil {
		return protocol.ChangeSet{}, err
	}
	_ = s.appendEvent(ctx, set, "restore_file", file.ID, "")
	return s.store.GetChangeSetForCheckpoint(ctx, sessionID, checkpointID)
}

func (s *Service) RestoreAll(ctx context.Context, sessionID, checkpointID string) (protocol.ChangeSet, error) {
	set, err := s.store.GetChangeSetForCheckpoint(ctx, sessionID, checkpointID)
	if err != nil {
		return protocol.ChangeSet{}, err
	}
	for _, file := range set.Files {
		if file.Status == protocol.RestoreRestored {
			continue
		}
		if _, err := s.RestoreFile(ctx, sessionID, checkpointID, file.ID); err != nil {
			return protocol.ChangeSet{}, err
		}
	}
	_ = s.appendEvent(ctx, set, "restore_all", "", "")
	return s.store.GetChangeSetForCheckpoint(ctx, sessionID, checkpointID)
}

func (s *Service) load(ctx context.Context, sessionID, checkpointID string) (protocol.AgentSession, protocol.ChangeSet, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return protocol.AgentSession{}, protocol.ChangeSet{}, err
	}
	if session.Status != protocol.SessionActive {
		return protocol.AgentSession{}, protocol.ChangeSet{}, ErrSessionClosed
	}
	set, err := s.store.GetChangeSetForCheckpoint(ctx, sessionID, checkpointID)
	return session, set, err
}

func (s *Service) verifyFile(ctx context.Context, repository string, set protocol.ChangeSet, file protocol.FileChange) error {
	if file.RestoreMode == "hunk" && file.Modified != nil {
		expected := []byte(*file.Modified)
		hasRestoredHunk := false
		for _, hunk := range file.Hunks {
			if hunk.Status != protocol.RestoreRestored {
				continue
			}
			hasRestoredHunk = true
			modified := []byte(hunk.ModifiedText)
			if len(modified) == 0 || bytes.Count(expected, modified) != 1 {
				return ErrConflict
			}
			expected = bytes.Replace(expected, modified, []byte(hunk.OriginalText), 1)
		}
		if hasRestoredHunk {
			path := pointerValue(file.NewPath)
			if path == "" {
				path = pointerValue(file.OldPath)
			}
			target, err := safePath(repository, path)
			if err != nil {
				return err
			}
			current, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(current, expected) {
				return ErrConflict
			}
			return nil
		}
	}
	current, err := s.snapshots.CaptureTree(ctx, repository)
	if err != nil {
		return err
	}
	paths := []string{}
	if file.OldPath != nil {
		paths = append(paths, *file.OldPath)
	}
	if file.NewPath != nil && pointerValue(file.NewPath) != pointerValue(file.OldPath) {
		paths = append(paths, *file.NewPath)
	}
	args := []string{"diff", "--quiet", set.AfterTree, current, "--"}
	args = append(args, paths...)
	cmd := exec.CommandContext(ctx, s.gitPath, append([]string{"-C", repository}, args...)...)
	if err := cmd.Run(); err != nil {
		return ErrConflict
	}
	return nil
}

func (s *Service) applyBefore(ctx context.Context, repository, beforeTree string, file protocol.FileChange) error {
	oldPath, newPath := pointerValue(file.OldPath), pointerValue(file.NewPath)
	if newPath != "" && newPath != oldPath {
		target, err := safePath(repository, newPath)
		if err != nil {
			return err
		}
		if err := removeFile(target); err != nil {
			return err
		}
	}
	if oldPath == "" {
		target, err := safePath(repository, newPath)
		if err != nil {
			return err
		}
		return removeFile(target)
	}
	mode, data, exists, err := s.readTreeEntry(ctx, repository, beforeTree, oldPath)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("before checkpoint entry is missing")
	}
	target, err := safePath(repository, oldPath)
	if err != nil {
		return err
	}
	if mode == "120000" {
		if err := removeFile(target); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.Symlink(string(data), target)
	}
	perm := os.FileMode(0o644)
	if mode == "100755" {
		perm = 0o755
	}
	return atomicWrite(target, data, perm)
}

func (s *Service) readTreeEntry(ctx context.Context, repository, tree, path string) (string, []byte, bool, error) {
	cmd := exec.CommandContext(ctx, s.gitPath, "-C", repository, "ls-tree", "-z", tree, "--", path)
	out, err := cmd.Output()
	if err != nil {
		return "", nil, false, err
	}
	if len(out) == 0 {
		return "", nil, false, nil
	}
	header, _, ok := bytes.Cut(out, []byte{'\t'})
	if !ok {
		return "", nil, false, errors.New("invalid ls-tree output")
	}
	fields := strings.Fields(string(header))
	if len(fields) < 3 {
		return "", nil, false, errors.New("invalid ls-tree entry")
	}
	show := exec.CommandContext(ctx, s.gitPath, "-C", repository, "show", tree+":"+path)
	data, err := show.Output()
	if err != nil {
		return "", nil, false, err
	}
	return fields[0], data, true, nil
}

func (s *Service) conflictHunk(ctx context.Context, set protocol.ChangeSet, hunkID string) error {
	_ = s.store.UpdateHunkRestore(ctx, set.CheckpointID, hunkID, protocol.RestoreConflict, s.now().UTC())
	return ErrConflict
}
func (s *Service) appendEvent(ctx context.Context, set protocol.ChangeSet, action, fileID, hunkID string) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"checkpointId": set.CheckpointID, "action": action, "fileId": fileID, "hunkId": hunkID})
	_, err = s.store.AppendEvent(ctx, protocol.SessionEvent{ID: id, SessionID: set.SessionID, RunID: &set.RunID, Timestamp: s.now().UTC(), SchemaVersion: protocol.SchemaVersion, Source: protocol.EventSourceUser, Type: protocol.EventTypeChangeRestored, Data: data})
	return err
}
func findFile(set protocol.ChangeSet, id string) *protocol.FileChange {
	for i := range set.Files {
		if set.Files[i].ID == id {
			return &set.Files[i]
		}
	}
	return nil
}
func findHunk(set protocol.ChangeSet, id string) (*protocol.FileChange, *protocol.ChangeHunk) {
	for i := range set.Files {
		for j := range set.Files[i].Hunks {
			if set.Files[i].Hunks[j].ID == id {
				return &set.Files[i], &set.Files[i].Hunks[j]
			}
		}
	}
	return nil, nil
}
func safePath(root, path string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(path)))
	if err != nil {
		return "", err
	}
	if target == rootAbs || !strings.HasPrefix(target, rootAbs+string(filepath.Separator)) {
		return "", errors.New("restore path escapes repository")
	}
	return target, nil
}
func removeFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if perm == 0 {
		if info, err := os.Stat(path); err == nil {
			perm = info.Mode().Perm()
		} else {
			perm = 0o644
		}
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".maatgen-restore-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Chmod(perm); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(name, path)
}
func pointerValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "event_" + hex.EncodeToString(b), nil
}
