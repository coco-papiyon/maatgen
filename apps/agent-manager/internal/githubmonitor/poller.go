package githubmonitor

import (
	"context"
	"fmt"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/githubapi"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

// GitHubClient is the subset of *githubapi.Client the Poller needs. It
// exists so tests can substitute a fake instead of hitting a real (or even
// fake HTTP) GitHub API.
type GitHubClient interface {
	ListIssues(ctx context.Context, owner, repo string, opts githubapi.ListOptions) ([]protocol.GitHubItem, error)
	ListPullRequests(ctx context.Context, owner, repo string, opts githubapi.ListOptions) ([]protocol.GitHubItem, error)
	FetchProjectFields(ctx context.Context, owner, repo string, kind protocol.GitHubItemKind, number int) ([]protocol.GitHubProjectFieldValue, error)
}

// ClientFactory builds a GitHubClient authenticated against host. See
// githubapi.ClientFactory for the concrete implementation main.go wires in.
type ClientFactory func(host string) (GitHubClient, error)

// PollerStore is the persistence dependency Poller needs beyond what
// Evaluator already requires.
type PollerStore interface {
	GetRepositoryMonitor(ctx context.Context, repository string) (protocol.GitHubRepositoryMonitor, error)
	UpdateRepositoryMonitorSyncState(ctx context.Context, repository string, lastSyncedAt, nextSyncAt time.Time, lastError *string, updatedAt time.Time) error
	// ApplyRemoteChange atomically updates the monitor's target, clears its
	// observed items, and resets its sync state, so an interrupted remote
	// switch is never partially visible after a restart (ADR-007 section 1).
	ApplyRemoteChange(ctx context.Context, monitor protocol.GitHubRepositoryMonitor, updatedAt time.Time) error
}

// SyncResult summarizes one repository sync, for the manual "sync now"
// action's response and logging.
type SyncResult struct {
	IssuesProcessed       int
	PullRequestsProcessed int
	EventsMatched         int
}

// RemoteCandidate represents a resolved GitHub remote from the local repository.
type RemoteCandidate struct {
	Host       string
	Owner      string
	Name       string
	RemoteName string
}

// RemoteResolveFunc revalidates a monitor's GitHub remote against the local
// repository's git remotes (ADR-007 section 1). It uses the monitor's
// RemoteName to track which remote was explicitly selected; if the remote
// has moved or been deleted, it detects this without falling back to
// automatic selection (which would violate ADR section 1: "選択したremoteを追跡").
type RemoteResolveFunc func(ctx context.Context, repository string, selectedRemoteName string) (*RemoteCandidate, error)

// Poller runs one GitHub polling cycle for a repository (ADR-007 section
// 2): fetch Issues and Pull Requests (and, per item, Project field values),
// then hand each to Evaluator to detect changes and match Trigger Rules.
// It is deliberately synchronous and stateless between calls; both the
// manual "sync now" action and a scheduled poll call the same
// SyncRepository method (see ADR-007 section 2: the two fetch paths share
// the adapter and this evaluation logic, not a persistence model).
type Poller struct {
	store          PollerStore
	evaluator      *Evaluator
	clients        ClientFactory
	remoteResolver RemoteResolveFunc
	now            func() time.Time
}

func NewPoller(store PollerStore, evaluator *Evaluator, clients ClientFactory, remoteResolver RemoteResolveFunc) *Poller {
	return &Poller{store: store, evaluator: evaluator, clients: clients, remoteResolver: remoteResolver, now: time.Now}
}

// SyncRepository fetches every Issue and Pull Request for repository's
// configured GitHub repository, evaluates each against its Trigger Rules,
// and records the sync outcome (ADR-007 section 3: lastSyncedAt, nextSyncAt,
// lastError). A fetch failure (auth, rate limit, network) is recorded as
// the monitor's lastError and returned; it does not panic or leave the
// monitor's sync state stale forever; a rate limit failure backs off by at
// least the GitHub-reported retry-after instead of the configured
// interval.
//
// Before fetching, it revalidates the monitor's remote against the local
// repository (ADR-007 section 1: "remote変更検出後は前のrepositoryへ問い合わせない").
// If the remote has changed, the monitor's target, observed items, and sync
// state are all updated atomically (PollerStore.ApplyRemoteChange) so the
// next sync treats the new target as a first sync with no prior
// observations, and the old repository is not queried again. Atomicity here
// specifically prevents a crash between these updates from leaving the
// monitor pointing at the new repository while still holding the old
// repository's observed items or a stale LastSyncedAt, which would cause
// the new target's items to be evaluated as spurious changes.
//
// State "all" is fetched (not just "open") so a transition to closed is
// observed even though Maatgen never received a webhook or event stream
// for it; for a repository with a very large Issue/PR history this means
// each poll is more expensive than fetching only open items. This is an
// accepted, documented v1 simplification, not a fundamental constraint: a
// future revision could instead diff the fetched open set against
// previously-observed open items and fetch closures individually.
func (p *Poller) SyncRepository(ctx context.Context, repository string) (SyncResult, error) {
	monitor, err := p.store.GetRepositoryMonitor(ctx, repository)
	if err != nil {
		return SyncResult{}, fmt.Errorf("sync github repository: %w", err)
	}

	candidate, err := p.remoteResolver(ctx, repository, monitor.RemoteName)
	if err != nil {
		p.recordFailure(ctx, monitor, err)
		return SyncResult{}, fmt.Errorf("sync github repository: revalidate remote: %w", err)
	}

	if candidate == nil {
		p.recordFailure(ctx, monitor, fmt.Errorf("github remote %q no longer exists or is inaccessible", monitor.RemoteName))
		return SyncResult{}, fmt.Errorf("sync github repository: remote resolution failed: remote %q lost", monitor.RemoteName)
	}
	if candidate.Host != monitor.Host || candidate.Owner != monitor.Owner || candidate.Name != monitor.Name {
		monitor.Host, monitor.Owner, monitor.Name, monitor.RemoteName = candidate.Host, candidate.Owner, candidate.Name, candidate.RemoteName
		monitor.LastSyncedAt, monitor.NextSyncAt = nil, nil
		if err := p.store.ApplyRemoteChange(ctx, monitor, p.now().UTC()); err != nil {
			p.recordFailure(ctx, monitor, fmt.Errorf("apply remote change: %w", err))
			return SyncResult{}, fmt.Errorf("sync github repository: apply remote change: %w", err)
		}
	}

	client, err := p.clients(monitor.Host)
	if err != nil {
		p.recordFailure(ctx, monitor, err)
		return SyncResult{}, fmt.Errorf("sync github repository: build client: %w", err)
	}

	var result SyncResult
	issues, err := client.ListIssues(ctx, monitor.Owner, monitor.Name, githubapi.ListOptions{State: "all"})
	if err != nil {
		p.recordFailure(ctx, monitor, err)
		return result, fmt.Errorf("sync github repository: list issues: %w", err)
	}
	for _, issue := range issues {
		issue = p.withProjectFields(ctx, client, monitor, issue)
		events, evalErr := p.evaluator.EvaluateItem(ctx, monitor, issue)
		if evalErr != nil {
			p.recordFailure(ctx, monitor, evalErr)
			return result, fmt.Errorf("sync github repository: evaluate issue #%d: %w", issue.Number, evalErr)
		}
		result.IssuesProcessed++
		result.EventsMatched += len(events)
	}

	pulls, err := client.ListPullRequests(ctx, monitor.Owner, monitor.Name, githubapi.ListOptions{State: "all"})
	if err != nil {
		p.recordFailure(ctx, monitor, err)
		return result, fmt.Errorf("sync github repository: list pull requests: %w", err)
	}
	for _, pull := range pulls {
		pull = p.withProjectFields(ctx, client, monitor, pull)
		events, evalErr := p.evaluator.EvaluateItem(ctx, monitor, pull)
		if evalErr != nil {
			p.recordFailure(ctx, monitor, evalErr)
			return result, fmt.Errorf("sync github repository: evaluate pull request #%d: %w", pull.Number, evalErr)
		}
		result.PullRequestsProcessed++
		result.EventsMatched += len(events)
	}

	now := p.now().UTC()
	next := now.Add(pollInterval(monitor))
	if err := p.store.UpdateRepositoryMonitorSyncState(ctx, repository, now, next, nil, now); err != nil {
		return result, fmt.Errorf("sync github repository: update sync state: %w", err)
	}
	return result, nil
}

// withProjectFields augments item with its Project field values. A
// failure is recorded on the item (ProjectsError), never on the monitor:
// ADR-007 section 2 requires a Projects fetch failure to never block
// Issue/PR monitoring.
func (p *Poller) withProjectFields(ctx context.Context, client GitHubClient, monitor protocol.GitHubRepositoryMonitor, item protocol.GitHubItem) protocol.GitHubItem {
	fields, err := client.FetchProjectFields(ctx, monitor.Owner, monitor.Name, item.Kind, item.Number)
	if err != nil {
		item.ProjectsError = err.Error()
		return item
	}
	item.ProjectFields = fields
	return item
}

func (p *Poller) recordFailure(ctx context.Context, monitor protocol.GitHubRepositoryMonitor, err error) {
	now := p.now().UTC()
	delay := pollInterval(monitor)
	if limit, ok := githubapi.AsRateLimit(err); ok && limit.RetryAfter > delay {
		delay = limit.RetryAfter
	}
	message := err.Error()
	_ = p.store.UpdateRepositoryMonitorSyncState(ctx, monitor.Repository, now, now.Add(delay), &message, now)
}

func pollInterval(monitor protocol.GitHubRepositoryMonitor) time.Duration {
	if monitor.PollIntervalSeconds <= 0 {
		return time.Minute
	}
	return time.Duration(monitor.PollIntervalSeconds) * time.Second
}
