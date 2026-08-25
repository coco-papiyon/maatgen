// Package gitworktree resolves the GitHub repository (host, owner, name)
// that a local Git repository points to, so that GitHub monitoring
// (ADR-007) can be configured without asking the user to enter that
// information twice.
package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"
)

// ErrNoGitHubRemote is returned when none of the repository's remotes point
// at github.com or an explicitly allowed GitHub Enterprise host.
var ErrNoGitHubRemote = errors.New("no GitHub remote found")

// GitHubRepository identifies a GitHub repository resolved from a local Git
// remote. Host is always lowercase. It never carries credentials: remote
// URL userinfo (e.g. https://user:token@host/...) is discarded during
// resolution and must never be logged or persisted.
type GitHubRepository struct {
	Host       string `json:"host"`
	Owner      string `json:"owner"`
	Name       string `json:"name"`
	RemoteName string `json:"remoteName"`
}

// RemoteResolution is the outcome of resolving a repository's remotes
// against the set of allowed GitHub hosts.
//
// Repository is nil when the target could not be determined automatically:
// either because multiple GitHub remotes exist and none of them is "origin"
// (ambiguous), or because Candidates is empty (see ErrNoGitHubRemote).
// Callers must not guess in that case; the caller is expected to let the
// user pick a remote from Candidates.
type RemoteResolution struct {
	Repository *GitHubRepository  `json:"repository,omitempty"`
	Candidates []GitHubRepository `json:"candidates"`
}

// ResolveGitHubRemote inspects the remotes configured on repository and
// determines which one, if any, points at a GitHub repository.
//
// Selection rules:
//   - Only remotes whose host is "github.com" or listed in allowedHosts
//     (a GitHub Enterprise allowlist, case-insensitive) are considered.
//   - If the "origin" remote is a GitHub remote, it is selected.
//   - Otherwise, if exactly one GitHub remote exists, it is selected.
//   - Otherwise (zero or multiple non-origin candidates), Repository is nil
//     and the caller must ask the user to choose.
func ResolveGitHubRemote(ctx context.Context, gitPath, repository string, allowedHosts []string) (RemoteResolution, error) {
	remotes, err := listRemoteURLs(ctx, gitPath, repository)
	if err != nil {
		return RemoteResolution{}, fmt.Errorf("list git remotes: %w", err)
	}

	names := make([]string, 0, len(remotes))
	for name := range remotes {
		names = append(names, name)
	}
	sort.Strings(names)

	var candidates []GitHubRepository
	for _, name := range names {
		host, owner, repoName, ok := normalizeGitHubURL(remotes[name])
		if !ok || !isAllowedGitHubHost(host, allowedHosts) {
			continue
		}
		candidates = append(candidates, GitHubRepository{Host: host, Owner: owner, Name: repoName, RemoteName: name})
	}

	if len(candidates) == 0 {
		return RemoteResolution{}, ErrNoGitHubRemote
	}
	for _, candidate := range candidates {
		if candidate.RemoteName == "origin" {
			selected := candidate
			return RemoteResolution{Repository: &selected, Candidates: candidates}, nil
		}
	}
	if len(candidates) == 1 {
		selected := candidates[0]
		return RemoteResolution{Repository: &selected, Candidates: candidates}, nil
	}
	return RemoteResolution{Candidates: candidates}, nil
}

// isAllowedGitHubHost reports whether host is github.com or matches an
// entry in allowedHosts. An entry of the form "*.example.com" matches
// example.com itself and any of its subdomains (e.g. "tenant.ghe.com" for
// the "*.ghe.com" entry used by GitHub Enterprise Cloud with data
// residency), so a single entry covers every tenant hostname without
// listing each one individually.
func isAllowedGitHubHost(host string, allowedHosts []string) bool {
	if strings.EqualFold(host, "github.com") {
		return true
	}
	host = strings.ToLower(host)
	for _, allowed := range allowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if suffix, isWildcard := strings.CutPrefix(allowed, "*."); isWildcard {
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

// normalizeGitHubURL parses a Git remote URL in any of the forms Git
// accepts (https://, http://, ssh://, git://, or the scp-like
// user@host:path shorthand) into a lowercase host and an owner/name pair.
// It strips a trailing ".git" suffix and trailing slashes. It deliberately
// discards any userinfo (credentials) embedded in the URL.
func normalizeGitHubURL(raw string) (host, owner, name string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", false
	}

	var hostPart, pathPart string
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" {
			return "", "", "", false
		}
		hostPart = parsed.Hostname()
		pathPart = parsed.Path
	} else if idx := strings.Index(raw, ":"); idx >= 0 {
		// scp-like shorthand: [user@]host:owner/name(.git)
		left, right := raw[:idx], raw[idx+1:]
		if at := strings.LastIndex(left, "@"); at >= 0 {
			hostPart = left[at+1:]
		} else {
			hostPart = left
		}
		pathPart = right
	} else {
		return "", "", "", false
	}

	hostPart = strings.ToLower(strings.TrimSpace(hostPart))
	pathPart = strings.Trim(pathPart, "/")
	pathPart = strings.TrimSuffix(pathPart, ".git")
	segments := strings.Split(pathPart, "/")
	if hostPart == "" || len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return "", "", "", false
	}
	return hostPart, segments[0], segments[1], true
}

// listRemoteURLs returns the configured remotes as a name -> URL map. A
// repository with no remotes configured yields an empty map, not an error.
func listRemoteURLs(ctx context.Context, gitPath, repository string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, gitPath, "-C", repository, "config", "--get-regexp", `^remote\..*\.url$`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && strings.TrimSpace(stdout.String()) == "" {
			return map[string]string{}, nil
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}

	const prefix, suffix = "remote.", ".url"
	remotes := make(map[string]string)
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, " ")
		if !found || !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
			continue
		}
		name := key[len(prefix) : len(key)-len(suffix)]
		remotes[name] = value
	}
	return remotes, nil
}
