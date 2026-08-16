package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/security"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

type Store interface {
	CreateApproval(context.Context, protocol.CommandApproval) error
	UpdateApprovalAssessment(context.Context, string, protocol.ApprovalRisk, float64, string, []string) error
	DecideApproval(context.Context, string, protocol.ApprovalStatus, protocol.ApprovalDecision, protocol.ApprovalScope, protocol.ApprovalSource, []string, time.Time) (protocol.CommandApproval, error)
	GetApproval(context.Context, string) (protocol.CommandApproval, error)
	ListApprovals(context.Context, string, *protocol.ApprovalStatus) ([]protocol.CommandApproval, error)
	GetRun(context.Context, string) (protocol.AgentRun, error)
	UpdateRun(context.Context, protocol.AgentRun) error
	AppendEvent(context.Context, protocol.SessionEvent) (protocol.SessionEvent, error)
}

type Assessment struct {
	Risk       protocol.ApprovalRisk `json:"risk"`
	Confidence float64               `json:"confidence"`
	Summary    string                `json:"summary"`
	Factors    []string              `json:"factors"`
}

type Reviewer interface {
	Review(context.Context, agent.ApprovalRequest, []protocol.CommandSegment) (Assessment, error)
}

type RuleSaver func([]string) error

type Config struct {
	Enabled           bool
	MaxRisk           protocol.ApprovalRisk
	MinConfidence     float64
	HumanTimeout      time.Duration
	ReviewerTimeout   time.Duration
	AllowedCommands   [][]string
	Reviewer          Reviewer
	SavePermanentRule RuleSaver
}

type Service struct {
	store Store
	cfg   Config

	mu           sync.RWMutex
	pending      map[string]chan protocol.CommandApproval
	sessionRules map[string][][]string
	allowed      [][]string
	humanSlots   map[string]chan struct{}
	reviewSlots  chan struct{}
	closed       bool
}

func New(store Store, cfg Config) *Service {
	if cfg.HumanTimeout <= 0 {
		cfg.HumanTimeout = 10 * time.Minute
	}
	if cfg.ReviewerTimeout <= 0 {
		cfg.ReviewerTimeout = 15 * time.Second
	}
	if cfg.MinConfidence == 0 {
		cfg.MinConfidence = 0.8
	}
	if cfg.MaxRisk == "" {
		cfg.MaxRisk = protocol.ApprovalRiskLow
	}
	return &Service{
		store: store, cfg: cfg, pending: map[string]chan protocol.CommandApproval{},
		sessionRules: map[string][][]string{}, allowed: cloneRules(cfg.AllowedCommands),
		humanSlots: map[string]chan struct{}{}, reviewSlots: make(chan struct{}, 1),
	}
}

func (s *Service) Handle(ctx context.Context, request agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	if !s.cfg.Enabled {
		return agent.ApprovalDecision{Approved: true, Reason: "command approval is disabled"}, nil
	}
	segments, parseErr := ParseCommand(request.Command)
	storedSegments := redactSegments(segments)
	approval := protocol.CommandApproval{
		ID: newID("approval"), SessionID: request.SessionID, RunID: request.RunID,
		ProviderRequestID: request.ProviderRequestID,
		Shell:             nonEmpty(request.Shell, "shell"), WorkingDirectory: request.WorkingDirectory,
		Command: security.RedactString(request.Command), Segments: storedSegments,
		Status: protocol.ApprovalPending, Factors: []string{}, CreatedAt: time.Now().UTC(),
	}
	if err := s.store.CreateApproval(context.WithoutCancel(ctx), approval); err != nil {
		return agent.ApprovalDecision{}, err
	}

	if parseErr == nil && AllSegmentsAllowed(segments, s.rulesForSession(request.SessionID)) {
		decided, err := s.decide(context.WithoutCancel(ctx), approval.ID, protocol.ApprovalAllowOnce, protocol.ApprovalScopeOnce, protocol.ApprovalSourceConfig, nil)
		if err != nil {
			return agent.ApprovalDecision{}, err
		}
		return agent.ApprovalDecision{Approved: true, Reason: decisionReason(decided)}, nil
	}

	if parseErr == nil && s.cfg.Reviewer != nil {
		reviewRequest := request
		reviewRequest.Command = approval.Command
		assessment, err := s.review(ctx, reviewRequest, storedSegments)
		if err == nil {
			_ = s.store.UpdateApprovalAssessment(context.WithoutCancel(ctx), approval.ID, assessment.Risk, assessment.Confidence, assessment.Summary, assessment.Factors)
			approval.Risk, approval.Confidence, approval.Summary, approval.Factors = &assessment.Risk, &assessment.Confidence, &assessment.Summary, assessment.Factors
			if assessment.Confidence >= s.cfg.MinConfidence && riskValue(assessment.Risk) <= riskValue(s.cfg.MaxRisk) && assessment.Risk != protocol.ApprovalRiskCritical {
				decided, decideErr := s.decide(context.WithoutCancel(ctx), approval.ID, protocol.ApprovalAllowOnce, protocol.ApprovalScopeOnce, protocol.ApprovalSourceAI, nil)
				if decideErr != nil {
					return agent.ApprovalDecision{}, decideErr
				}
				return agent.ApprovalDecision{Approved: true, Reason: decisionReason(decided)}, nil
			}
		}
	}

	return s.waitForHuman(ctx, approval)
}

func (s *Service) waitForHuman(ctx context.Context, approval protocol.CommandApproval) (agent.ApprovalDecision, error) {
	decisionChannel := make(chan protocol.CommandApproval, 1)
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.pending[approval.ID] = decisionChannel
	}
	s.mu.Unlock()
	if closed {
		_, _ = s.systemDecision(context.Background(), approval.ID, protocol.ApprovalCancelled)
		return agent.ApprovalDecision{Approved: false, Reason: "approval service is closed"}, nil
	}
	defer func() {
		s.mu.Lock()
		delete(s.pending, approval.ID)
		s.mu.Unlock()
	}()

	slot := s.humanSlot(approval.SessionID)
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	case <-ctx.Done():
		_, _ = s.systemDecision(context.Background(), approval.ID, protocol.ApprovalCancelled)
		return agent.ApprovalDecision{Approved: false, Reason: "run cancelled"}, ctx.Err()
	}
	select {
	case decided := <-decisionChannel:
		return agent.ApprovalDecision{Approved: decided.Status == protocol.ApprovalApproved, Reason: decisionReason(decided)}, nil
	default:
	}

	if err := s.setRunWaiting(context.WithoutCancel(ctx), approval.RunID, true); err != nil {
		return agent.ApprovalDecision{}, err
	}
	_ = s.appendEvent(context.WithoutCancel(ctx), approval, protocol.EventTypeCommandApprovalRequested)
	timer := time.NewTimer(s.cfg.HumanTimeout)
	defer timer.Stop()
	select {
	case decided := <-decisionChannel:
		_ = s.setRunWaiting(context.WithoutCancel(ctx), approval.RunID, false)
		return agent.ApprovalDecision{Approved: decided.Status == protocol.ApprovalApproved, Reason: decisionReason(decided)}, nil
	case <-ctx.Done():
		_, _ = s.systemDecision(context.Background(), approval.ID, protocol.ApprovalCancelled)
		_ = s.setRunWaiting(context.Background(), approval.RunID, false)
		return agent.ApprovalDecision{Approved: false, Reason: "run cancelled"}, ctx.Err()
	case <-timer.C:
		_, _ = s.systemDecision(context.Background(), approval.ID, protocol.ApprovalExpired)
		_ = s.setRunWaiting(context.Background(), approval.RunID, false)
		return agent.ApprovalDecision{Approved: false, Reason: "approval timed out"}, nil
	}
}

func (s *Service) Decide(ctx context.Context, sessionID, id string, request protocol.ApprovalDecisionRequest) (protocol.CommandApproval, error) {
	approval, err := s.store.GetApproval(ctx, id)
	if err != nil {
		return protocol.CommandApproval{}, err
	}
	if approval.SessionID != sessionID {
		return protocol.CommandApproval{}, storage.ErrNotFound
	}
	if approval.Status != protocol.ApprovalPending {
		return protocol.CommandApproval{}, storage.ErrConflict
	}
	scope := protocol.ApprovalScopeOnce
	status := protocol.ApprovalApproved
	rule := append([]string(nil), request.RuleArgv...)
	switch request.Decision {
	case protocol.ApprovalAllowOnce:
	case protocol.ApprovalAllowSession, protocol.ApprovalAllowPermanent:
		if request.Decision == protocol.ApprovalAllowSession {
			scope = protocol.ApprovalScopeSession
		} else {
			scope = protocol.ApprovalScopePermanent
		}
		if len(rule) == 0 && len(approval.Segments) == 1 {
			rule = append([]string(nil), approval.Segments[0].Argv...)
		}
		if !ruleMatchesAny(rule, approval.Segments) {
			return protocol.CommandApproval{}, fmt.Errorf("%w: ruleArgv must match an approval segment", storage.ErrConflict)
		}
		if scope == protocol.ApprovalScopePermanent {
			for _, value := range rule {
				if strings.Contains(value, "***") {
					return protocol.CommandApproval{}, fmt.Errorf("%w: replace redacted secret arguments with * before saving", storage.ErrConflict)
				}
			}
			if s.cfg.SavePermanentRule == nil {
				return protocol.CommandApproval{}, errors.New("permanent approval rules are not configured")
			}
			if err := s.cfg.SavePermanentRule(rule); err != nil {
				return protocol.CommandApproval{}, err
			}
		}
	case protocol.ApprovalDeny:
		status = protocol.ApprovalDenied
	default:
		return protocol.CommandApproval{}, errors.New("invalid approval decision")
	}
	decided, err := s.store.DecideApproval(ctx, id, status, request.Decision, scope, protocol.ApprovalSourceHuman, rule, time.Now().UTC())
	if err != nil {
		return protocol.CommandApproval{}, err
	}
	if status == protocol.ApprovalApproved && len(rule) > 0 {
		s.mu.Lock()
		if scope == protocol.ApprovalScopeSession {
			s.sessionRules[approval.SessionID] = append(s.sessionRules[approval.SessionID], append([]string(nil), rule...))
		}
		if scope == protocol.ApprovalScopePermanent {
			s.allowed = append(s.allowed, append([]string(nil), rule...))
		}
		s.mu.Unlock()
	}
	s.mu.RLock()
	channel := s.pending[id]
	s.mu.RUnlock()
	if channel != nil {
		select {
		case channel <- decided:
		default:
		}
	}
	_ = s.appendEvent(context.WithoutCancel(ctx), decided, protocol.EventTypeCommandApprovalDecided)
	return decided, nil
}

func (s *Service) List(ctx context.Context, sessionID string, pendingOnly bool) (protocol.ApprovalListResponse, error) {
	var status *protocol.ApprovalStatus
	if pendingOnly {
		value := protocol.ApprovalPending
		status = &value
	}
	approvals, err := s.store.ListApprovals(ctx, sessionID, status)
	return protocol.ApprovalListResponse{Approvals: approvals}, err
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	pending := make(map[string]chan protocol.CommandApproval, len(s.pending))
	for id, channel := range s.pending {
		pending[id] = channel
	}
	s.mu.Unlock()
	for id, channel := range pending {
		decided, err := s.systemDecision(context.Background(), id, protocol.ApprovalCancelled)
		if err != nil {
			decided = protocol.CommandApproval{ID: id, Status: protocol.ApprovalCancelled}
		}
		select {
		case channel <- decided:
		default:
		}
	}
}

func (s *Service) review(ctx context.Context, request agent.ApprovalRequest, segments []protocol.CommandSegment) (Assessment, error) {
	select {
	case s.reviewSlots <- struct{}{}:
		defer func() { <-s.reviewSlots }()
	case <-ctx.Done():
		return Assessment{}, ctx.Err()
	}
	reviewCtx, cancel := context.WithTimeout(ctx, s.cfg.ReviewerTimeout)
	defer cancel()
	return s.cfg.Reviewer.Review(reviewCtx, request, segments)
}

func (s *Service) decide(ctx context.Context, id string, decision protocol.ApprovalDecision, scope protocol.ApprovalScope, source protocol.ApprovalSource, rule []string) (protocol.CommandApproval, error) {
	status := protocol.ApprovalApproved
	if decision == protocol.ApprovalDeny {
		status = protocol.ApprovalDenied
	}
	decided, err := s.store.DecideApproval(ctx, id, status, decision, scope, source, rule, time.Now().UTC())
	if err == nil {
		_ = s.appendEvent(context.WithoutCancel(ctx), decided, protocol.EventTypeCommandApprovalDecided)
	}
	return decided, err
}

func (s *Service) systemDecision(ctx context.Context, id string, status protocol.ApprovalStatus) (protocol.CommandApproval, error) {
	decided, err := s.store.DecideApproval(ctx, id, status, protocol.ApprovalDeny, protocol.ApprovalScopeOnce, protocol.ApprovalSourceSystem, nil, time.Now().UTC())
	if err == nil {
		_ = s.appendEvent(context.WithoutCancel(ctx), decided, protocol.EventTypeCommandApprovalDecided)
	}
	return decided, err
}

func (s *Service) setRunWaiting(ctx context.Context, runID string, waiting bool) error {
	run, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if waiting {
		run.Status = protocol.RunWaitingForApproval
	} else if run.Status == protocol.RunWaitingForApproval {
		run.Status = protocol.RunRunning
	}
	return s.store.UpdateRun(ctx, run)
}

func (s *Service) appendEvent(ctx context.Context, approval protocol.CommandApproval, eventType string) error {
	data, err := json.Marshal(approval)
	if err != nil {
		return err
	}
	_, err = s.store.AppendEvent(ctx, protocol.SessionEvent{
		ID: newID("event"), SessionID: approval.SessionID, RunID: &approval.RunID,
		Timestamp: time.Now().UTC(), SchemaVersion: protocol.SchemaVersion,
		Source: protocol.EventSourceManager, Type: eventType, Data: data,
	})
	return err
}

func (s *Service) rulesForSession(sessionID string) [][]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := cloneRules(s.allowed)
	result = append(result, cloneRules(s.sessionRules[sessionID])...)
	return result
}

func (s *Service) humanSlot(sessionID string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.humanSlots[sessionID] == nil {
		s.humanSlots[sessionID] = make(chan struct{}, 1)
	}
	return s.humanSlots[sessionID]
}

func cloneRules(rules [][]string) [][]string {
	result := make([][]string, len(rules))
	for i := range rules {
		result[i] = append([]string(nil), rules[i]...)
	}
	return result
}

func redactSegments(segments []protocol.CommandSegment) []protocol.CommandSegment {
	result := make([]protocol.CommandSegment, len(segments))
	for index, segment := range segments {
		result[index] = segment
		result[index].Command = security.RedactString(segment.Command)
		result[index].Argv = make([]string, len(segment.Argv))
		maskNext := false
		for argumentIndex, argument := range segment.Argv {
			if maskNext {
				result[index].Argv[argumentIndex] = "***"
				maskNext = false
				continue
			}
			result[index].Argv[argumentIndex] = security.RedactString(argument)
			maskNext = sensitiveArgumentFlag(argument)
		}
	}
	return result
}

func sensitiveArgumentFlag(argument string) bool {
	name := strings.TrimLeft(strings.ToLower(strings.TrimSpace(argument)), "-")
	name = strings.NewReplacer("_", "", "-", "").Replace(name)
	return name == "apikey" || name == "accesstoken" || name == "refreshtoken" || name == "password" || name == "authorization"
}
func ruleMatchesAny(rule []string, segments []protocol.CommandSegment) bool {
	if len(rule) == 0 {
		return false
	}
	for _, segment := range segments {
		if MatchArgv(rule, segment.Argv) {
			return true
		}
	}
	return false
}
func riskValue(risk protocol.ApprovalRisk) int {
	switch risk {
	case protocol.ApprovalRiskSafe:
		return 0
	case protocol.ApprovalRiskLow:
		return 1
	case protocol.ApprovalRiskHigh:
		return 2
	default:
		return 3
	}
}
func decisionReason(approval protocol.CommandApproval) string {
	if approval.Source == nil {
		return string(approval.Status)
	}
	return fmt.Sprintf("%s by %s", approval.Status, *approval.Source)
}
func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func newID(prefix string) string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(buffer)
}
