package gitworktree

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestNormalizeGitHubURL(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		host  string
		owner string
		repo  string
		ok    bool
	}{
		{"https", "https://github.com/octo-org/example.git", "github.com", "octo-org", "example", true},
		{"https no suffix", "https://github.com/octo-org/example", "github.com", "octo-org", "example", true},
		{"https trailing slash", "https://github.com/octo-org/example/", "github.com", "octo-org", "example", true},
		{"https with credentials", "https://user:token@github.com/octo-org/example.git", "github.com", "octo-org", "example", true},
		{"ssh scp-like", "git@github.com:octo-org/example.git", "github.com", "octo-org", "example", true},
		{"ssh url", "ssh://git@github.com/octo-org/example.git", "github.com", "octo-org", "example", true},
		{"ssh url with port", "ssh://git@github.com:22/octo-org/example.git", "github.com", "octo-org", "example", true},
		{"enterprise host", "https://github.example.com/octo-org/example.git", "github.example.com", "octo-org", "example", true},
		{"uppercase host normalized", "https://GitHub.com/octo-org/example.git", "github.com", "octo-org", "example", true},
		{"empty", "", "", "", "", false},
		{"no path", "https://github.com/", "", "", "", false},
		{"only owner", "https://github.com/octo-org", "", "", "", false},
		{"not a url", "not-a-url", "", "", "", false},
		{"too many segments", "https://github.com/octo-org/example/extra", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, owner, repo, ok := normalizeGitHubURL(tc.raw)
			if ok != tc.ok || host != tc.host || owner != tc.owner || repo != tc.repo {
				t.Fatalf("normalizeGitHubURL(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
					tc.raw, host, owner, repo, ok, tc.host, tc.owner, tc.repo, tc.ok)
			}
		})
	}
}

func TestIsAllowedGitHubHost(t *testing.T) {
	if !isAllowedGitHubHost("github.com", nil) {
		t.Fatalf("github.com should always be allowed")
	}
	if isAllowedGitHubHost("github.example.com", nil) {
		t.Fatalf("unlisted enterprise host must not be allowed")
	}
	if !isAllowedGitHubHost("github.example.com", []string{"other.invalid", "GitHub.Example.com"}) {
		t.Fatalf("enterprise host listed (case-insensitively) in allowedHosts should be allowed")
	}
}

func TestResolveGitHubRemotePrefersOrigin(t *testing.T) {
	repo := t.TempDir()
	gitPath := requireGit(t)
	runGit(t, gitPath, repo, "init")
	runGit(t, gitPath, repo, "remote", "add", "origin", "https://github.com/octo-org/example.git")
	runGit(t, gitPath, repo, "remote", "add", "upstream", "https://github.com/other-org/example.git")

	resolution, err := ResolveGitHubRemote(context.Background(), gitPath, repo, nil)
	if err != nil {
		t.Fatalf("ResolveGitHubRemote: %v", err)
	}
	if resolution.Repository == nil {
		t.Fatalf("expected origin to be selected, got nil")
	}
	if resolution.Repository.RemoteName != "origin" || resolution.Repository.Owner != "octo-org" {
		t.Fatalf("selected = %#v", resolution.Repository)
	}
	if len(resolution.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want 2", resolution.Candidates)
	}
}

func TestResolveGitHubRemoteSingleNonOriginCandidate(t *testing.T) {
	repo := t.TempDir()
	gitPath := requireGit(t)
	runGit(t, gitPath, repo, "init")
	runGit(t, gitPath, repo, "remote", "add", "upstream", "git@github.com:octo-org/example.git")

	resolution, err := ResolveGitHubRemote(context.Background(), gitPath, repo, nil)
	if err != nil {
		t.Fatalf("ResolveGitHubRemote: %v", err)
	}
	if resolution.Repository == nil || resolution.Repository.RemoteName != "upstream" {
		t.Fatalf("selected = %#v", resolution.Repository)
	}
}

func TestResolveGitHubRemoteAmbiguousWithoutOrigin(t *testing.T) {
	repo := t.TempDir()
	gitPath := requireGit(t)
	runGit(t, gitPath, repo, "init")
	runGit(t, gitPath, repo, "remote", "add", "a", "https://github.com/octo-org/example.git")
	runGit(t, gitPath, repo, "remote", "add", "b", "https://github.com/other-org/example.git")

	resolution, err := ResolveGitHubRemote(context.Background(), gitPath, repo, nil)
	if err != nil {
		t.Fatalf("ResolveGitHubRemote: %v", err)
	}
	if resolution.Repository != nil {
		t.Fatalf("expected ambiguous resolution (nil Repository), got %#v", resolution.Repository)
	}
	if len(resolution.Candidates) != 2 {
		t.Fatalf("candidates = %#v, want 2", resolution.Candidates)
	}
}

func TestResolveGitHubRemoteNoGitHubRemote(t *testing.T) {
	repo := t.TempDir()
	gitPath := requireGit(t)
	runGit(t, gitPath, repo, "init")
	runGit(t, gitPath, repo, "remote", "add", "origin", "https://gitlab.com/octo-org/example.git")

	_, err := ResolveGitHubRemote(context.Background(), gitPath, repo, nil)
	if !errors.Is(err, ErrNoGitHubRemote) {
		t.Fatalf("err = %v, want ErrNoGitHubRemote", err)
	}
}

func TestResolveGitHubRemoteNoRemotesConfigured(t *testing.T) {
	repo := t.TempDir()
	gitPath := requireGit(t)
	runGit(t, gitPath, repo, "init")

	_, err := ResolveGitHubRemote(context.Background(), gitPath, repo, nil)
	if !errors.Is(err, ErrNoGitHubRemote) {
		t.Fatalf("err = %v, want ErrNoGitHubRemote", err)
	}
}

func TestResolveGitHubRemoteEnterpriseHostRequiresAllowlist(t *testing.T) {
	repo := t.TempDir()
	gitPath := requireGit(t)
	runGit(t, gitPath, repo, "init")
	runGit(t, gitPath, repo, "remote", "add", "origin", "https://github.example.com/octo-org/example.git")

	if _, err := ResolveGitHubRemote(context.Background(), gitPath, repo, nil); !errors.Is(err, ErrNoGitHubRemote) {
		t.Fatalf("err = %v, want ErrNoGitHubRemote when host is not allowlisted", err)
	}

	resolution, err := ResolveGitHubRemote(context.Background(), gitPath, repo, []string{"github.example.com"})
	if err != nil {
		t.Fatalf("ResolveGitHubRemote: %v", err)
	}
	if resolution.Repository == nil || resolution.Repository.Host != "github.example.com" {
		t.Fatalf("selected = %#v", resolution.Repository)
	}
}

func requireGit(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git not available: %v", err)
	}
	return gitPath
}

func runGit(t *testing.T, gitPath, repository string, args ...string) {
	t.Helper()
	cmd := exec.Command(gitPath, append([]string{"-C", repository}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
