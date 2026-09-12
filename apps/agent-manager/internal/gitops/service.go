package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

var (
	ErrNotRepository = errors.New("workspace is not a Git repository")
	ErrRunActive     = errors.New("session has an active run")
	ErrNoChanges     = errors.New("there are no changes to commit")
	ErrNoUpstream    = errors.New("current branch has no remote to push to")
	ErrDetachedHEAD  = errors.New("cannot push a detached HEAD")
	ErrEmptyMessage  = errors.New("commit message is required")
)

type SessionReader interface {
	GetSession(ctx context.Context, id string) (protocol.AgentSession, error)
}

type Service struct {
	sessions SessionReader
	gitPath  string
}

func New(sessions SessionReader, gitPath string) *Service {
	if gitPath == "" {
		gitPath = "git"
	}
	return &Service{sessions: sessions, gitPath: gitPath}
}

func (s *Service) GetStatus(ctx context.Context, sessionID string) (protocol.GitStatus, error) {
	session, err := s.repositorySession(ctx, sessionID, false)
	if err != nil {
		return protocol.GitStatus{}, err
	}
	return s.status(ctx, session)
}

func (s *Service) Commit(ctx context.Context, sessionID, message string) (protocol.GitStatus, error) {
	if strings.TrimSpace(message) == "" {
		return protocol.GitStatus{}, ErrEmptyMessage
	}
	session, err := s.repositorySession(ctx, sessionID, true)
	if err != nil {
		return protocol.GitStatus{}, err
	}
	status, err := s.status(ctx, session)
	if err != nil {
		return protocol.GitStatus{}, err
	}
	if len(status.Files) == 0 {
		return protocol.GitStatus{}, ErrNoChanges
	}
	if _, err := s.run(ctx, session.Workspace, "add", "-A"); err != nil {
		return protocol.GitStatus{}, err
	}
	if _, err := s.run(ctx, session.Workspace, "commit", "-m", message); err != nil {
		return protocol.GitStatus{}, err
	}
	return s.status(ctx, session)
}

func (s *Service) Push(ctx context.Context, sessionID string) (protocol.GitStatus, error) {
	session, err := s.repositorySession(ctx, sessionID, true)
	if err != nil {
		return protocol.GitStatus{}, err
	}
	status, err := s.status(ctx, session)
	if err != nil {
		return protocol.GitStatus{}, err
	}
	if status.Branch == "" {
		return protocol.GitStatus{}, ErrDetachedHEAD
	}
	if status.Upstream != "" {
		if _, err := s.run(ctx, session.Workspace, "push"); err != nil {
			return protocol.GitStatus{}, err
		}
	} else {
		remote := status.RemoteName
		if remote == "" {
			return protocol.GitStatus{}, ErrNoUpstream
		}
		if _, err := s.run(ctx, session.Workspace, "push", "--set-upstream", remote, status.Branch); err != nil {
			return protocol.GitStatus{}, err
		}
	}
	return s.status(ctx, session)
}

func (s *Service) repositorySession(ctx context.Context, sessionID string, mutate bool) (protocol.AgentSession, error) {
	session, err := s.sessions.GetSession(ctx, sessionID)
	if err != nil {
		return protocol.AgentSession{}, err
	}
	if session.WorkspaceKind != protocol.WorkspaceGitRepository {
		return protocol.AgentSession{}, ErrNotRepository
	}
	if mutate && session.ActiveRunStatus != nil {
		return protocol.AgentSession{}, ErrRunActive
	}
	return session, nil
}

func (s *Service) status(ctx context.Context, session protocol.AgentSession) (protocol.GitStatus, error) {
	output, err := s.run(ctx, session.Workspace, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return protocol.GitStatus{}, err
	}
	result := protocol.GitStatus{SessionID: session.ID, Files: parsePorcelain(output)}
	if branch, branchErr := s.run(ctx, session.Workspace, "symbolic-ref", "--quiet", "--short", "HEAD"); branchErr == nil {
		result.Branch = strings.TrimSpace(string(branch))
	}
	if upstream, upstreamErr := s.run(ctx, session.Workspace, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); upstreamErr == nil {
		result.Upstream = strings.TrimSpace(string(upstream))
		result.RemoteName = strings.SplitN(result.Upstream, "/", 2)[0]
		if counts, countErr := s.run(ctx, session.Workspace, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); countErr == nil {
			parts := strings.Fields(string(counts))
			if len(parts) == 2 {
				result.Ahead, _ = strconv.Atoi(parts[0])
				result.Behind, _ = strconv.Atoi(parts[1])
			}
		}
	} else {
		result.RemoteName = s.defaultRemote(ctx, session.Workspace)
	}
	if result.RemoteName != "" {
		if remoteURL, remoteErr := s.run(ctx, session.Workspace, "remote", "get-url", result.RemoteName); remoteErr == nil {
			result.RemoteURL = sanitizeRemoteURL(strings.TrimSpace(string(remoteURL)))
		}
	}
	return result, nil
}

func sanitizeRemoteURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	parsed.User = nil
	return parsed.String()
}

func (s *Service) defaultRemote(ctx context.Context, repository string) string {
	output, err := s.run(ctx, repository, "remote")
	if err != nil {
		return ""
	}
	remotes := strings.Fields(string(output))
	for _, remote := range remotes {
		if remote == "origin" {
			return remote
		}
	}
	if len(remotes) == 1 {
		return remotes[0]
	}
	return ""
}

func parsePorcelain(output []byte) []protocol.GitStatusFile {
	records := bytes.Split(output, []byte{0})
	files := make([]protocol.GitStatusFile, 0, len(records))
	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) < 4 {
			continue
		}
		file := protocol.GitStatusFile{Path: string(record[3:])}
		if record[0] != ' ' {
			file.IndexStatus = string(record[0])
		}
		if record[1] != ' ' {
			file.WorktreeStatus = string(record[1])
		}
		if record[0] == 'R' || record[0] == 'C' || record[1] == 'R' || record[1] == 'C' {
			if index+1 < len(records) {
				index++
				file.OriginalPath = string(records[index])
			}
		}
		files = append(files, file)
	}
	return files
}

func (s *Service) run(ctx context.Context, repository string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, s.gitPath, append([]string{"-C", repository}, args...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], message)
	}
	return output, nil
}
