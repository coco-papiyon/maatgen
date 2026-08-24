// Package githubapi is the sole caller of GitHub's REST and GraphQL APIs
// (ADR-007 section 2). It confines GitHub-specific HTTP, pagination,
// authentication, rate limiting, and response normalization here so that no
// other package needs to know how GitHub's APIs work.
//
// A Client never logs or exposes the token it was constructed with; callers
// must apply the same discipline to the errors and data it returns, since
// Issue/PR body text is untrusted external input (ADR-007 section 2).
package githubapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v69/github"
	"github.com/shurcooL/githubv4"
)

// ErrAuthenticationRequired indicates that no usable GitHub credential was
// configured for the requested API operation.
var ErrAuthenticationRequired = errors.New("github authentication is required")

// Client accesses the GitHub REST and GraphQL APIs for a single
// authenticated identity against a single host (github.com or one GitHub
// Enterprise Server instance).
type Client struct {
	rest    *github.Client
	graphql *githubv4.Client
	Host    string
}

// NewClient builds a Client for host, which must be "github.com" (or empty,
// treated the same way) or a GitHub Enterprise Server hostname. Callers are
// expected to have already validated host against an explicit allowlist
// (see gitworktree.ResolveGitHubRemote) before calling this.
func NewClient(host, token string) (*Client, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("%w: run `gh auth login --scopes \"read:project\"` or `gh auth refresh -s read:project`, or configure github.token", ErrAuthenticationRequired)
	}
	httpClient := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &bearerTransport{token: token},
	}
	if host == "" || host == "github.com" {
		return &Client{
			rest:    github.NewClient(httpClient),
			graphql: githubv4.NewClient(httpClient),
			Host:    "github.com",
		}, nil
	}
	restBase := fmt.Sprintf("https://%s/api/v3/", host)
	uploadBase := fmt.Sprintf("https://%s/api/uploads/", host)
	rest, err := github.NewClient(httpClient).WithEnterpriseURLs(restBase, uploadBase)
	if err != nil {
		return nil, fmt.Errorf("configure GitHub Enterprise REST client: %w", err)
	}
	return &Client{
		rest:    rest,
		graphql: githubv4.NewEnterpriseClient(fmt.Sprintf("https://%s/api/graphql", host), httpClient),
		Host:    host,
	}, nil
}

// ClientFactory returns a constructor of per-host Clients that all
// authenticate with token. ADR-007 does not require per-host tokens, only a
// per-host allowlist for which hosts may be targeted at all (enforced
// earlier, by gitworktree.ResolveGitHubRemote), so a single configured
// token is used regardless of which allowed host is requested.
func ClientFactory(token string) func(host string) (*Client, error) {
	return func(host string) (*Client, error) {
		return NewClient(host, token)
	}
}

// bearerTransport injects the GitHub token as a bearer credential on every
// outgoing request. It is applied uniformly to both the REST and GraphQL
// HTTP clients so authentication logic lives in exactly one place.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Authorization", "Bearer "+t.token)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}
