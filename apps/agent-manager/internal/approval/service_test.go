package approval

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage/sqlite"
)

type fixedReviewer struct {
	assessment       Assessment
	approvedCommands [][]string
}

func (reviewer *fixedReviewer) Review(_ context.Context, _ agent.ApprovalRequest, _ []protocol.CommandSegment, approvedCommands [][]string) (Assessment, error) {
	reviewer.approvedCommands = approvedCommands
	return reviewer.assessment, nil
}

func TestServiceUsesConfigRuleBeforeReviewer(t *testing.T) {
	store := approvalTestStore(t)
	reviewer := &fixedReviewer{assessment: Assessment{Risk: protocol.ApprovalRiskCritical, Confidence: 1}}
	service := New(store, Config{Enabled: true, AllowedCommands: [][]string{{"git", "status"}}, Reviewer: reviewer})

	decision, err := service.Handle(context.Background(), approvalRequest("git status"))
	if err != nil || !decision.Approved {
		t.Fatalf("decision = %#v, err = %v", decision, err)
	}
	approvals, err := store.ListApprovals(context.Background(), "session-1", nil)
	if err != nil || len(approvals) != 1 || approvals[0].Source == nil || *approvals[0].Source != protocol.ApprovalSourceConfig {
		t.Fatalf("approvals = %#v, err = %v", approvals, err)
	}
}

func TestServiceAllowsLowRiskAIReview(t *testing.T) {
	store := approvalTestStore(t)
	service := New(store, Config{
		Enabled: true, MaxRisk: protocol.ApprovalRiskLow, MinConfidence: .8,
		Reviewer: &fixedReviewer{assessment: Assessment{Risk: protocol.ApprovalRiskLow, Confidence: .95, Summary: "read-only inspection"}},
	})

	decision, err := service.Handle(context.Background(), approvalRequest("git log -1"))
	if err != nil || !decision.Approved {
		t.Fatalf("decision = %#v, err = %v", decision, err)
	}
	approvals, _ := store.ListApprovals(context.Background(), "session-1", nil)
	if len(approvals) != 1 || approvals[0].Source == nil || *approvals[0].Source != protocol.ApprovalSourceAI {
		t.Fatalf("approval source = %#v", approvals)
	}
}

func TestServiceReviewerReceivesSessionAndPermanentlyApprovedCommands(t *testing.T) {
	store := approvalTestStore(t)
	reviewer := &fixedReviewer{assessment: Assessment{Risk: protocol.ApprovalRiskLow, Confidence: .95, Summary: "matches an approved pattern"}}
	service := New(store, Config{
		Enabled: true, MaxRisk: protocol.ApprovalRiskLow, MinConfidence: .8,
		AllowedCommands: [][]string{{"git", "status"}}, Reviewer: reviewer,
	})
	service.mu.Lock()
	service.sessionRules["session-1"] = [][]string{{"go", "test", "*"}}
	service.mu.Unlock()

	decision, err := service.Handle(context.Background(), approvalRequest("go build ./..."))
	if err != nil || !decision.Approved {
		t.Fatalf("decision = %#v, err = %v", decision, err)
	}
	want := [][]string{{"git", "status"}, {"go", "test", "*"}}
	if !reflect.DeepEqual(reviewer.approvedCommands, want) {
		t.Fatalf("approvedCommands = %#v, want %#v", reviewer.approvedCommands, want)
	}
}

func TestServiceWaitsForHumanAndAddsSessionRule(t *testing.T) {
	store := approvalTestStore(t)
	service := New(store, Config{Enabled: true, HumanTimeout: time.Second})
	result := make(chan agent.ApprovalDecision, 1)
	errors := make(chan error, 1)
	go func() {
		decision, err := service.Handle(context.Background(), approvalRequest("go test ./internal/approval"))
		result <- decision
		errors <- err
	}()

	approval := waitForPendingApproval(t, store)
	waitForRunStatus(t, store, "run-1", protocol.RunWaitingForApproval)
	_, err := service.Decide(context.Background(), "session-1", approval.ID, protocol.ApprovalDecisionRequest{
		Decision: protocol.ApprovalAllowSession,
		RuleArgv: []string{"go", "test", "*"},
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if err := <-errors; err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if decision := <-result; !decision.Approved {
		t.Fatalf("decision = %#v", decision)
	}

	secondRequest := approvalRequest("go test ./internal/server")
	secondRequest.ProviderRequestID = "provider-2"
	decision, err := service.Handle(context.Background(), secondRequest)
	if err != nil || !decision.Approved {
		t.Fatalf("session rule decision = %#v, err = %v", decision, err)
	}
}

func TestServiceMarksIndividualSegmentsAllowedForHumanReview(t *testing.T) {
	store := approvalTestStore(t)
	service := New(store, Config{Enabled: true, HumanTimeout: time.Second, AllowedCommands: [][]string{{"git", "status"}}})
	result := make(chan agent.ApprovalDecision, 1)
	go func() {
		decision, _ := service.Handle(context.Background(), approvalRequest("git status && go test ./internal/approval"))
		result <- decision
	}()

	approval := waitForPendingApproval(t, store)
	if len(approval.Segments) != 2 {
		t.Fatalf("segments = %#v", approval.Segments)
	}
	if !approval.Segments[0].Allowed {
		t.Errorf("expected first segment to already match AllowedCommands: %#v", approval.Segments[0])
	}
	if approval.Segments[1].Allowed {
		t.Errorf("expected second segment to still need a decision: %#v", approval.Segments[1])
	}

	if _, err := service.Decide(context.Background(), "session-1", approval.ID, protocol.ApprovalDecisionRequest{Decision: protocol.ApprovalDeny}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	<-result
}

func TestServiceExpiresUnansweredHumanApproval(t *testing.T) {
	store := approvalTestStore(t)
	service := New(store, Config{Enabled: true, HumanTimeout: 20 * time.Millisecond})
	decision, err := service.Handle(context.Background(), approvalRequest("curl --password plain-secret https://example.test"))
	if err != nil || decision.Approved || decision.Reason != "approval timed out" {
		t.Fatalf("decision = %#v, err = %v", decision, err)
	}
	approvals, err := store.ListApprovals(context.Background(), "session-1", nil)
	if err != nil || len(approvals) != 1 || approvals[0].Status != protocol.ApprovalExpired {
		t.Fatalf("approvals = %#v, err = %v", approvals, err)
	}
	if strings.Contains(approvals[0].Command, "plain-secret") || slices.Contains(approvals[0].Segments[0].Argv, "plain-secret") {
		t.Fatalf("approval retained a secret: %#v", approvals[0])
	}
}

func approvalTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := protocol.AgentSession{ID: "session-1", Agent: protocol.AgentCodex, Workspace: t.TempDir(), Status: protocol.SessionActive, CreatedAt: time.Now().UTC()}
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.CreateRun(context.Background(), protocol.AgentRun{ID: "run-1", SessionID: session.ID, Status: protocol.RunRunning, Prompt: "test"}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return store
}

func approvalRequest(command string) agent.ApprovalRequest {
	return agent.ApprovalRequest{SessionID: "session-1", RunID: "run-1", ProviderRequestID: "provider-1", Command: command, Shell: "powershell", WorkingDirectory: "C:/workspace"}
}

func waitForPendingApproval(t *testing.T, store *sqlite.Store) protocol.CommandApproval {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	status := protocol.ApprovalPending
	for time.Now().Before(deadline) {
		approvals, err := store.ListApprovals(context.Background(), "session-1", &status)
		if err == nil && len(approvals) > 0 {
			return approvals[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pending approval did not appear")
	return protocol.CommandApproval{}
}

func waitForRunStatus(t *testing.T, store *sqlite.Store, runID string, expected protocol.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		run, err := store.GetRun(context.Background(), runID)
		if err == nil && run.Status == expected {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, err := store.GetRun(context.Background(), runID)
	t.Fatalf("run status = %q, err = %v", run.Status, err)
}
