package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

var (
	ErrInvalidRequest   = errors.New("invalid session request")
	ErrUnsupportedAgent = errors.New("unsupported agent")
	ErrRunActive        = errors.New("session has an active run")
	ErrCleanupFailed    = errors.New("checkpoint cleanup failed")
)

type Store interface {
	CreateSession(ctx context.Context, session protocol.AgentSession) error
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
	CloseSession(ctx context.Context, id string, closedAt time.Time) error
	ReopenSession(ctx context.Context, id string) error
	ReplaceSourceStats(ctx context.Context, stats protocol.SourceStats) error
	// CloseMonitorEventForSession closes the GitHub monitor event (Job) that
	// created this session, if any, cascading a Session close into closing
	// its Job (Issue #34). It is a no-op when no event references the
	// session.
	CloseMonitorEventForSession(ctx context.Context, sessionID string, closedAt time.Time) error
}

type RepositoryManager interface {
	ValidateRepository(ctx context.Context, workspace string) (string, error)
	CleanupSession(ctx context.Context, repository, sessionID string) error
}

// SourceStatsAnalyzer counts source lines per language for a repository. It
// runs once, at Session creation, not on every Run.
type SourceStatsAnalyzer interface {
	Analyze(ctx context.Context, repository string) (protocol.SourceStats, error)
}

type Service struct {
	store        Store
	repositories RepositoryManager
	analyzer     SourceStatsAnalyzer
	now          func() time.Time
	newID        func() (string, error)
	wg           sync.WaitGroup
}

// waitForSourceStats blocks until all in-flight background source stats
// analyses have finished. It exists for tests; production callers don't need
// to wait since the analysis is best-effort and asynchronous.
func (s *Service) waitForSourceStats() { s.wg.Wait() }

type Option func(*Service)

func WithSourceStatsAnalyzer(analyzer SourceStatsAnalyzer) Option {
	return func(s *Service) { s.analyzer = analyzer }
}

func New(store Store, repositories RepositoryManager, options ...Option) *Service {
	service := &Service{store: store, repositories: repositories, now: time.Now, newID: generateID}
	for _, option := range options {
		option(service)
	}
	return service
}

func (s *Service) CreateSession(ctx context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error) {
	switch request.Agent {
	case protocol.AgentCodex, protocol.AgentClaude, protocol.AgentCopilot:
	default:
		return protocol.AgentSession{}, ErrUnsupportedAgent
	}
	if strings.TrimSpace(request.Workspace) == "" {
		return protocol.AgentSession{}, fmt.Errorf("%w: workspace is required", ErrInvalidRequest)
	}
	repository, err := s.repositories.ValidateRepository(ctx, request.Workspace)
	if err != nil {
		return protocol.AgentSession{}, err
	}
	id, err := s.newID()
	if err != nil {
		return protocol.AgentSession{}, fmt.Errorf("generate session ID: %w", err)
	}
	triggerSource := request.TriggerSource
	if triggerSource == "" {
		triggerSource = protocol.TriggerSourceManual
	}
	created := protocol.AgentSession{
		ID: id, Agent: request.Agent, Workspace: repository,
		Status: protocol.SessionActive, TriggerSource: triggerSource,
		GitHubMonitorEvent: request.GitHubMonitorEvent,
		GitHubRuleID:       request.GitHubRuleID,
		GitHubItemKind:     request.GitHubItemKind,
		GitHubItemNumber:   request.GitHubItemNumber,
		CreatedAt:          s.now().UTC(),
	}
	if err := s.store.CreateSession(ctx, created); err != nil {
		return protocol.AgentSession{}, fmt.Errorf("persist session: %w", err)
	}
	if s.analyzer != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.recordSourceStats(context.Background(), created.ID, repository)
		}()
	}
	return created, nil
}

// recordSourceStats counts source lines once, at Session creation. It runs in
// the background so a slow or failing cloc invocation (e.g. against a large
// or cloud-synced directory) never blocks session creation. It is
// best-effort: a failed count must not fail session creation.
func (s *Service) recordSourceStats(ctx context.Context, sessionID, repository string) {
	stats, err := s.analyzer.Analyze(ctx, repository)
	if err != nil {
		slog.Warn("source stats analysis failed", "session", sessionID, "repository", repository, "error", err)
		return
	}
	stats.SessionID = sessionID
	if err := s.store.ReplaceSourceStats(ctx, stats); err != nil {
		slog.Warn("save source stats failed", "session", sessionID, "error", err)
	}
}

func (s *Service) CloseSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	if strings.TrimSpace(id) == "" {
		return protocol.AgentSession{}, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	session, err := s.store.GetSession(ctx, id)
	if err != nil {
		return protocol.AgentSession{}, err
	}
	if session.Status == protocol.SessionActive {
		if err := s.store.CloseSession(ctx, id, s.now().UTC()); errors.Is(err, storage.ErrRunActive) {
			return protocol.AgentSession{}, ErrRunActive
		} else if err != nil {
			return protocol.AgentSession{}, err
		}
	}
	if err := s.store.CloseMonitorEventForSession(ctx, id, s.now().UTC()); err != nil {
		return protocol.AgentSession{}, fmt.Errorf("close github monitor event for session: %w", err)
	}
	if err := s.repositories.CleanupSession(ctx, session.Workspace, session.ID); err != nil {
		return protocol.AgentSession{}, fmt.Errorf("%w: %v", ErrCleanupFailed, err)
	}
	return s.store.GetSession(ctx, id)
}

func (s *Service) ReopenSession(ctx context.Context, id string) (protocol.AgentSession, error) {
	if strings.TrimSpace(id) == "" {
		return protocol.AgentSession{}, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	if err := s.store.ReopenSession(ctx, id); err != nil {
		return protocol.AgentSession{}, err
	}
	return s.store.GetSession(ctx, id)
}

func (s *Service) SearchWorkspaceFiles(ctx context.Context, sessionID, query string) ([]string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return searchFiles(session.Workspace, strings.ToLower(strings.TrimSpace(query)), 2000, 20), nil
}

// maxWorkspaceTreeNodes bounds how many entries GetWorkspaceFileTree will
// return for a single directory, so an unusually large directory can't
// produce an unbounded response.
const maxWorkspaceTreeNodes = 5000

// maxWorkspaceFileBytes bounds how much of a file ReadWorkspaceFile reads,
// so opening an accidental multi-gigabyte file can't exhaust memory.
const maxWorkspaceFileBytes = 1 << 20 // 1 MiB

var excludedWorkspaceDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "out": true, "build": true,
	".next": true, "coverage": true, ".venv": true, "__pycache__": true,
}

// GetWorkspaceFileTree lists the immediate contents of a directory within a
// session's workspace (relPath == "" lists the workspace root). It does not
// recurse: callers fetch a subdirectory's contents on demand, using the
// HasChildren hint to avoid a request for directories known to be empty.
func (s *Service) GetWorkspaceFileTree(ctx context.Context, sessionID, relPath string) ([]protocol.WorkspaceFileNode, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	cleanPath := cleanWorkspaceRelPath(relPath)
	dir := session.Workspace
	if cleanPath != "" {
		dir = filepath.Join(session.Workspace, filepath.FromSlash(cleanPath))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	nodes := make([]protocol.WorkspaceFileNode, 0, len(entries))
	for _, entry := range entries {
		if len(nodes) >= maxWorkspaceTreeNodes {
			break
		}
		if entry.IsDir() && excludedWorkspaceDirs[entry.Name()] {
			continue
		}
		childPath := entry.Name()
		if cleanPath != "" {
			childPath = cleanPath + "/" + entry.Name()
		}
		if entry.IsDir() {
			nodes = append(nodes, protocol.WorkspaceFileNode{
				Name: entry.Name(), Path: childPath, Type: "dir",
				HasChildren: directoryHasVisibleEntries(filepath.Join(dir, entry.Name())),
			})
			continue
		}
		nodes = append(nodes, protocol.WorkspaceFileNode{Name: entry.Name(), Path: childPath, Type: "file"})
	}
	return nodes, nil
}

func directoryHasVisibleEntries(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && excludedWorkspaceDirs[entry.Name()] {
			continue
		}
		return true
	}
	return false
}

// cleanWorkspaceRelPath cleans a client-supplied relative path against a
// synthetic root, so "../" segments collapse instead of escaping the
// workspace once joined with it. An empty/whitespace input maps to "".
func cleanWorkspaceRelPath(relPath string) string {
	trimmed := strings.TrimSpace(relPath)
	if trimmed == "" {
		return ""
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+filepath.ToSlash(trimmed)), "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

// ReadWorkspaceFile returns the text content of a file within a session's
// workspace. relPath is cleaned against a synthetic root before joining it
// with the workspace directory, so "../" segments can't escape it.
func (s *Service) ReadWorkspaceFile(ctx context.Context, sessionID, relPath string) (protocol.WorkspaceFileContent, error) {
	if strings.TrimSpace(sessionID) == "" {
		return protocol.WorkspaceFileContent{}, fmt.Errorf("%w: session ID is required", ErrInvalidRequest)
	}
	cleanPath := cleanWorkspaceRelPath(relPath)
	if cleanPath == "" {
		return protocol.WorkspaceFileContent{}, fmt.Errorf("%w: file path is required", ErrInvalidRequest)
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return protocol.WorkspaceFileContent{}, err
	}
	absPath := filepath.Join(session.Workspace, filepath.FromSlash(cleanPath))
	info, err := os.Stat(absPath)
	if err != nil {
		return protocol.WorkspaceFileContent{}, err
	}
	if info.IsDir() {
		return protocol.WorkspaceFileContent{}, fmt.Errorf("%w: path is a directory", ErrInvalidRequest)
	}
	file, err := os.Open(absPath)
	if err != nil {
		return protocol.WorkspaceFileContent{}, err
	}
	defer file.Close()
	truncated := info.Size() > maxWorkspaceFileBytes
	readSize := info.Size()
	if truncated {
		readSize = maxWorkspaceFileBytes
	}
	buffer := make([]byte, readSize)
	n, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return protocol.WorkspaceFileContent{}, err
	}
	data := buffer[:n]
	if isBinaryContent(data) {
		return protocol.WorkspaceFileContent{Path: cleanPath, Binary: true}, nil
	}
	return protocol.WorkspaceFileContent{Path: cleanPath, Content: string(data), Truncated: truncated}, nil
}

func isBinaryContent(data []byte) bool {
	if !utf8.Valid(data) {
		return true
	}
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func generateID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "session_" + hex.EncodeToString(random), nil
}

func searchFiles(root, query string, maxCandidates, maxResults int) []string {
	exclude := map[string]bool{
		"node_modules": true, ".git": true, "dist": true, "out": true, "build": true,
		".next": true, "coverage": true, ".venv": true, "__pycache__": true,
	}
	var files []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || len(files) >= maxCandidates {
			return nil
		}
		if d.IsDir() && exclude[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})

	if query == "" {
		sort.Strings(files)
		if len(files) > maxResults {
			files = files[:maxResults]
		}
		makeRelative(root, files)
		return files
	}

	type scoredFile struct {
		file  string
		score int
	}
	var scoredFiles []scoredFile
	for _, f := range files {
		if s := scoreFileMatch(f, query); s >= 0 {
			scoredFiles = append(scoredFiles, scoredFile{file: f, score: s})
		}
	}

	sort.Slice(scoredFiles, func(i, j int) bool {
		if scoredFiles[i].score != scoredFiles[j].score {
			return scoredFiles[i].score < scoredFiles[j].score
		}
		return len(scoredFiles[i].file) < len(scoredFiles[j].file)
	})

	result := make([]string, 0, len(scoredFiles))
	for _, s := range scoredFiles {
		if len(result) >= maxResults {
			break
		}
		result = append(result, s.file)
	}
	makeRelative(root, result)
	return result
}

func scoreFileMatch(path, query string) int {
	lower := strings.ToLower(path)
	basename := strings.ToLower(filepath.Base(path))
	if basename == query {
		return 0
	}
	if strings.HasPrefix(basename, query) {
		return 1
	}
	if strings.Contains(basename, query) {
		return 2
	}
	if strings.Contains(lower, query) {
		return 3
	}
	return -1
}

func makeRelative(root string, files []string) {
	for i, f := range files {
		if rel, err := filepath.Rel(root, f); err == nil {
			files[i] = rel
		}
	}
}
