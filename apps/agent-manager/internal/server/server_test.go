package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/checkpoint"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
	restoreservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/restore"
	runservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/run"
	sessionservice "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/session"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/storage"
)

func TestHealth(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)

	New(testConfig(), nil, nil).Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var response HealthResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "ok" {
		t.Fatalf("health status = %q, want ok", response.Status)
	}
	if response.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", response.SchemaVersion)
	}
}

func TestRuntimeConfig(t *testing.T) {
	config := testConfig()
	config.DefaultWorkspace = "C:/projects/example"
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/runtime-config"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response RuntimeConfigResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.DefaultWorkspace != "C:/projects/example" {
		t.Fatalf("default workspace = %q, want C:/projects/example", response.DefaultWorkspace)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/runtime-config", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}
}

func TestProviderCatalog(t *testing.T) {
	config := testConfig()
	config.Providers = []protocol.Provider{{ID: protocol.AgentCodex, Label: "Codex", Models: []string{"model-a"}}}
	recorder := httptest.NewRecorder()
	New(config, nil, nil).Handler().ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/providers"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.ProviderListResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Providers) != 1 || response.Providers[0].ID != protocol.AgentCodex || response.Providers[0].Models[0] != "model-a" {
		t.Fatalf("response = %#v", response)
	}
}

func TestNotFoundUsesCommonErrorEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil)

	New(testConfig(), nil, nil).Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}

	var response struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "not_found" || response.Error.Message == "" {
		t.Fatalf("error response = %#v", response)
	}
}

func TestSessionListAndDetail(t *testing.T) {
	createdAt := time.Date(2026, 8, 15, 3, 0, 0, 0, time.UTC)
	session := protocol.AgentSession{
		ID:        "session-1",
		Agent:     protocol.AgentCodex,
		Workspace: "C:/workspace/project",
		Status:    protocol.SessionActive,
		CreatedAt: createdAt,
	}
	reader := &fakeSessionReader{sessions: []protocol.AgentSession{session}}
	handler := New(testConfig(), reader, nil).Handler()

	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, authorizedRequest(http.MethodGet, "/api/v1/sessions?limit=10"))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRecorder.Code)
	}
	var list sessionListResponse
	if err := json.NewDecoder(listRecorder.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != session.ID || reader.limit != 11 {
		t.Fatalf("list = %#v, limit = %d", list, reader.limit)
	}

	detailRecorder := httptest.NewRecorder()
	handler.ServeHTTP(detailRecorder, authorizedRequest(http.MethodGet, "/api/v1/sessions/session-1"))
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status = %d", detailRecorder.Code)
	}
	var detail protocol.AgentSession
	if err := json.NewDecoder(detailRecorder.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.ID != session.ID || reader.requestedID != session.ID {
		t.Fatalf("detail = %#v, requested id = %q", detail, reader.requestedID)
	}
}

func TestSessionAPIValidationAndNotFound(t *testing.T) {
	reader := &fakeSessionReader{err: storage.ErrNotFound}
	handler := New(testConfig(), reader, nil).Handler()

	invalidRecorder := httptest.NewRecorder()
	handler.ServeHTTP(invalidRecorder, authorizedRequest(http.MethodGet, "/api/v1/sessions?limit=invalid"))
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d", invalidRecorder.Code)
	}

	notFoundRecorder := httptest.NewRecorder()
	handler.ServeHTTP(notFoundRecorder, authorizedRequest(http.MethodGet, "/api/v1/sessions/missing"))
	if notFoundRecorder.Code != http.StatusNotFound {
		t.Fatalf("not found status = %d", notFoundRecorder.Code)
	}
}

func TestSessionListCursor(t *testing.T) {
	createdAt := time.Date(2026, 8, 15, 3, 0, 0, 0, time.UTC)
	reader := &fakeSessionReader{sessions: []protocol.AgentSession{
		{ID: "session-2", Agent: protocol.AgentCodex, Status: protocol.SessionActive, CreatedAt: createdAt},
		{ID: "session-1", Agent: protocol.AgentCodex, Status: protocol.SessionClosed, CreatedAt: createdAt.Add(-time.Minute)},
	}}
	handler := New(testConfig(), reader, nil).Handler()

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, authorizedRequest(http.MethodGet, "/api/v1/sessions?limit=1"))
	var page sessionListResponse
	if first.Code != http.StatusOK {
		t.Fatalf("first page status = %d", first.Code)
	}
	if err := json.NewDecoder(first.Body).Decode(&page); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(page.Sessions) != 1 || page.NextCursor == "" {
		t.Fatalf("first page = %#v", page)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, authorizedRequest(http.MethodGet, "/api/v1/sessions?limit=1&cursor="+page.NextCursor))
	if second.Code != http.StatusOK || reader.cursor == nil || reader.cursor.ID != "session-2" || !reader.cursor.CreatedAt.Equal(createdAt) {
		t.Fatalf("second page status = %d, cursor = %#v", second.Code, reader.cursor)
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, authorizedRequest(http.MethodGet, "/api/v1/sessions?cursor=not-a-cursor"))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status = %d", invalid.Code)
	}
}

func TestSessionAPIRequiresBearerToken(t *testing.T) {
	handler := New(testConfig(), &fakeSessionReader{}, nil).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestCreateSessionAPI(t *testing.T) {
	created := protocol.AgentSession{
		ID: "session-1", Agent: protocol.AgentCodex, Workspace: "C:/repository",
		Status: protocol.SessionActive, CreatedAt: time.Date(2026, 8, 15, 6, 0, 0, 0, time.UTC),
	}
	creator := &fakeSessionCreator{created: created}
	config := testConfig()
	config.SessionCreator = creator
	handler := New(config, nil, nil).Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authorizedJSONRequest(http.MethodPost, "/api/v1/sessions", `{
		"agent":"codex","workspace":"C:/repository"
	}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.AgentSession
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != created.ID || creator.request.Workspace != "C:/repository" {
		t.Fatalf("response = %#v, request = %#v", response, creator.request)
	}
}

func TestCreateSessionAPIValidationAndDomainErrors(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		err    error
		status int
		code   string
	}{
		{name: "invalid JSON", body: `{`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "unknown field", body: `{"agent":"codex","workspace":"C:/repository","extra":true}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "unsupported agent", body: `{"agent":"claude","workspace":"C:/repository"}`, err: sessionservice.ErrUnsupportedAgent, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "not repository", body: `{"agent":"codex","workspace":"C:/directory"}`, err: checkpoint.ErrNotRepository, status: http.StatusUnprocessableEntity, code: "not_git_repository"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig()
			config.SessionCreator = &fakeSessionCreator{err: test.err}
			recorder := httptest.NewRecorder()
			New(config, nil, nil).Handler().ServeHTTP(
				recorder,
				authorizedJSONRequest(http.MethodPost, "/api/v1/sessions", test.body),
			)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.status, recorder.Body.String())
			}
			var response protocol.APIErrorResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Error.Code != test.code {
				t.Fatalf("code = %q, want %q", response.Error.Code, test.code)
			}
		})
	}
}

func TestCloseSessionAPI(t *testing.T) {
	closedAt := time.Date(2026, 8, 15, 7, 0, 0, 0, time.UTC)
	closer := &fakeSessionCloser{closed: protocol.AgentSession{
		ID: "session-1", Agent: protocol.AgentCodex, Workspace: "C:/repository",
		Status: protocol.SessionClosed, CreatedAt: closedAt.Add(-time.Hour), ClosedAt: &closedAt,
	}}
	config := testConfig()
	config.SessionCloser = closer
	recorder := httptest.NewRecorder()
	New(config, nil, nil).Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/sessions/session-1/close"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.AgentSession
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Status != protocol.SessionClosed || closer.sessionID != "session-1" {
		t.Fatalf("response = %#v, requested = %q", response, closer.sessionID)
	}
}

func TestCloseSessionAPIErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "missing", err: storage.ErrNotFound, status: http.StatusNotFound, code: "not_found"},
		{name: "active run", err: sessionservice.ErrRunActive, status: http.StatusConflict, code: "run_already_active"},
		{name: "cleanup failed", err: sessionservice.ErrCleanupFailed, status: http.StatusServiceUnavailable, code: "checkpoint_cleanup_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testConfig()
			config.SessionCloser = &fakeSessionCloser{err: test.err}
			recorder := httptest.NewRecorder()
			New(config, nil, nil).Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/sessions/session-1/close"))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			var response protocol.APIErrorResponse
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != test.code {
				t.Fatalf("code = %q, want %q", response.Error.Code, test.code)
			}
		})
	}
}

func TestStartRunAndCancelAPI(t *testing.T) {
	created := protocol.AgentRun{
		ID: "run-1", SessionID: "session-1", Status: protocol.RunQueued, Prompt: "fix the tests",
	}
	controller := &fakeRunController{created: created}
	config := testConfig()
	config.RunController = controller
	handler := New(config, nil, nil).Handler()

	startRecorder := httptest.NewRecorder()
	handler.ServeHTTP(startRecorder, authorizedJSONRequest(http.MethodPost, "/api/v1/sessions/session-1/messages", `{
		"message":"fix the tests","model":"gpt-5","timeoutSeconds":120
	}`))
	if startRecorder.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, body = %s", startRecorder.Code, startRecorder.Body.String())
	}
	var response protocol.AgentRun
	if err := json.NewDecoder(startRecorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != created.ID || controller.sessionID != "session-1" {
		t.Fatalf("response = %#v, session id = %q", response, controller.sessionID)
	}
	if controller.request.Message != "fix the tests" || controller.request.Model == nil || *controller.request.Model != "gpt-5" {
		t.Fatalf("request = %#v", controller.request)
	}

	cancelRecorder := httptest.NewRecorder()
	handler.ServeHTTP(cancelRecorder, authorizedRequest(http.MethodPost, "/api/v1/runs/run-1/cancel"))
	if cancelRecorder.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, body = %s", cancelRecorder.Code, cancelRecorder.Body.String())
	}
	if controller.cancelledID != "run-1" {
		t.Fatalf("cancelled id = %q", controller.cancelledID)
	}
}

func TestRunAPIValidationAndDomainErrors(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		err    error
		status int
	}{
		{name: "invalid JSON", body: `{`, status: http.StatusBadRequest},
		{name: "active run", body: `{"message":"next"}`, err: runservice.ErrRunActive, status: http.StatusConflict},
		{name: "closed session", body: `{"message":"next"}`, err: runservice.ErrSessionClosed, status: http.StatusConflict},
		{name: "missing session", body: `{"message":"next"}`, err: storage.ErrNotFound, status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := &fakeRunController{startErr: test.err}
			config := testConfig()
			config.RunController = controller
			recorder := httptest.NewRecorder()
			New(config, nil, nil).Handler().ServeHTTP(
				recorder,
				authorizedJSONRequest(http.MethodPost, "/api/v1/sessions/session-1/messages", test.body),
			)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}

	controller := &fakeRunController{cancelErr: runservice.ErrRunNotActive}
	config := testConfig()
	config.RunController = controller
	recorder := httptest.NewRecorder()
	New(config, nil, nil).Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/runs/run-1/cancel"))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("cancel status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

func TestGetChangeSetAPI(t *testing.T) {
	path := "changed.txt"
	reader := &fakeChangeReader{changeSet: protocol.ChangeSet{
		SessionID: "session-1", RunID: "run-1", CheckpointID: "checkpoint-1", BeforeTree: "before", AfterTree: "after",
		Files: []protocol.FileChange{
			{ID: "file-1", NewPath: &path, Kind: protocol.FileAdd, RestoreMode: "file", Status: protocol.RestoreChanged, Hunks: []protocol.ChangeHunk{}},
		},
	}}
	config := testConfig()
	config.ChangeReader = reader
	recorder := httptest.NewRecorder()
	New(config, nil, nil).Handler().ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/sessions/session-1/changes"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response protocol.ChangeSet
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.SessionID != "session-1" || len(response.Files) != 1 || reader.sessionID != "session-1" {
		t.Fatalf("response = %#v, requested = %q", response, reader.sessionID)
	}

	reader.err = storage.ErrNotFound
	notFound := httptest.NewRecorder()
	New(config, nil, nil).Handler().ServeHTTP(notFound, authorizedRequest(http.MethodGet, "/api/v1/sessions/missing/changes"))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("not found status = %d", notFound.Code)
	}
}

func TestRestoreAPI(t *testing.T) {
	controller := &fakeRestoreController{changeSet: protocol.ChangeSet{SessionID: "session-1", CheckpointID: "checkpoint-1", Files: []protocol.FileChange{}}}
	config := testConfig()
	config.RestoreController = controller
	handler := New(config, nil, nil).Handler()

	hunk := httptest.NewRecorder()
	handler.ServeHTTP(hunk, authorizedRequest(http.MethodPost, "/api/v1/sessions/session-1/checkpoints/checkpoint-1/hunks/hunk-1/restore"))
	if hunk.Code != http.StatusOK || controller.operation != "hunk" || controller.sessionID != "session-1" || controller.changeID != "hunk-1" {
		t.Fatalf("hunk status = %d, controller = %#v", hunk.Code, controller)
	}
	file := httptest.NewRecorder()
	handler.ServeHTTP(file, authorizedRequest(http.MethodPost, "/api/v1/sessions/session-1/checkpoints/checkpoint-1/files/file-1/restore"))
	if file.Code != http.StatusOK || controller.operation != "file" || controller.changeID != "file-1" {
		t.Fatalf("file status = %d, controller = %#v", file.Code, controller)
	}
	all := httptest.NewRecorder()
	handler.ServeHTTP(all, authorizedRequest(http.MethodPost, "/api/v1/sessions/session-1/checkpoints/checkpoint-1/restore"))
	if all.Code != http.StatusOK || controller.operation != "all" || controller.checkpointID != "checkpoint-1" {
		t.Fatalf("all status = %d, controller = %#v", all.Code, controller)
	}
}

func TestRestoreAPIErrors(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{err: storage.ErrNotFound, status: http.StatusNotFound, code: "not_found"},
		{err: restoreservice.ErrConflict, status: http.StatusConflict, code: "checkpoint_conflict"},
		{err: restoreservice.ErrSessionClosed, status: http.StatusConflict, code: "session_closed"},
		{err: restoreservice.ErrNotRestorable, status: http.StatusUnprocessableEntity, code: "not_restorable"},
	}
	for _, test := range tests {
		controller := &fakeRestoreController{err: test.err}
		config := testConfig()
		config.RestoreController = controller
		recorder := httptest.NewRecorder()
		New(config, nil, nil).Handler().ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/sessions/session-1/checkpoints/checkpoint-1/hunks/hunk-1/restore"))
		if recorder.Code != test.status {
			t.Fatalf("status = %d, want %d", recorder.Code, test.status)
		}
		var response protocol.APIErrorResponse
		if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil || response.Error.Code != test.code {
			t.Fatalf("response = %#v, err = %v", response, err)
		}
	}
}

func TestCommandApprovalAPI(t *testing.T) {
	controller := &fakeApprovalController{approval: protocol.CommandApproval{
		ID: "approval-1", SessionID: "session-1", RunID: "run-1", ProviderRequestID: "provider-1",
		Command: "go test ./...", Shell: "powershell", WorkingDirectory: "C:/workspace",
		Status: protocol.ApprovalPending, Factors: []string{}, CreatedAt: time.Now().UTC(),
	}}
	config := testConfig()
	config.ApprovalController = controller
	handler := New(config, nil, nil).Handler()

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, authorizedRequest(http.MethodGet, "/api/v1/sessions/session-1/approvals?status=pending"))
	if list.Code != http.StatusOK || controller.sessionID != "session-1" || !controller.pendingOnly {
		t.Fatalf("list status = %d, controller = %#v", list.Code, controller)
	}

	decision := httptest.NewRecorder()
	handler.ServeHTTP(decision, authorizedJSONRequest(http.MethodPost, "/api/v1/sessions/session-1/approvals/approval-1/decision", `{"decision":"allow_session","ruleArgv":["go","test","*"]}`))
	if decision.Code != http.StatusOK || controller.approvalID != "approval-1" || controller.request.Decision != protocol.ApprovalAllowSession {
		t.Fatalf("decision status = %d, controller = %#v, body = %s", decision.Code, controller, decision.Body.String())
	}
}

func TestCommandApprovalDecisionConflict(t *testing.T) {
	controller := &fakeApprovalController{err: storage.ErrConflict}
	config := testConfig()
	config.ApprovalController = controller
	recorder := httptest.NewRecorder()
	New(config, nil, nil).Handler().ServeHTTP(recorder, authorizedJSONRequest(http.MethodPost, "/api/v1/sessions/session-1/approvals/approval-1/decision", `{"decision":"deny"}`))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func testConfig() Config {
	return Config{Version: "test", SchemaVersion: 1, AuthToken: "test-token"}
}

func authorizedRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer test-token")
	return request
}

func authorizedJSONRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	return request
}

type fakeSessionReader struct {
	sessions    []protocol.AgentSession
	err         error
	limit       int
	cursor      *protocol.SessionCursor
	requestedID string
}

func (f *fakeSessionReader) ListSessions(_ context.Context, limit int, cursor *protocol.SessionCursor) ([]protocol.AgentSession, error) {
	f.limit = limit
	f.cursor = cursor
	return f.sessions, f.err
}

func (f *fakeSessionReader) GetSession(_ context.Context, id string) (protocol.AgentSession, error) {
	f.requestedID = id
	if f.err != nil {
		return protocol.AgentSession{}, f.err
	}
	for _, session := range f.sessions {
		if session.ID == id {
			return session, nil
		}
	}
	return protocol.AgentSession{}, storage.ErrNotFound
}

var _ SessionReader = (*fakeSessionReader)(nil)

type fakeSessionCreator struct {
	request protocol.CreateSessionRequest
	created protocol.AgentSession
	err     error
}

type fakeSessionCloser struct {
	sessionID string
	closed    protocol.AgentSession
	err       error
}

func (f *fakeSessionCloser) CloseSession(_ context.Context, id string) (protocol.AgentSession, error) {
	f.sessionID = id
	return f.closed, f.err
}

var _ SessionCloser = (*fakeSessionCloser)(nil)

type fakeRunController struct {
	sessionID   string
	request     protocol.SendMessageRequest
	created     protocol.AgentRun
	startErr    error
	cancelledID string
	cancelErr   error
}

func (f *fakeRunController) StartRun(_ context.Context, sessionID string, request protocol.SendMessageRequest) (protocol.AgentRun, error) {
	f.sessionID = sessionID
	f.request = request
	return f.created, f.startErr
}

func (f *fakeRunController) CancelRun(_ context.Context, runID string) error {
	f.cancelledID = runID
	return f.cancelErr
}

var _ RunController = (*fakeRunController)(nil)

type fakeChangeReader struct {
	sessionID string
	changeSet protocol.ChangeSet
	err       error
}

func (f *fakeChangeReader) GetChangeSet(_ context.Context, sessionID string) (protocol.ChangeSet, error) {
	f.sessionID = sessionID
	return f.changeSet, f.err
}

var _ ChangeReader = (*fakeChangeReader)(nil)

type fakeRestoreController struct {
	operation    string
	sessionID    string
	checkpointID string
	changeID     string
	changeSet    protocol.ChangeSet
	err          error
}

func (f *fakeRestoreController) RestoreHunk(_ context.Context, sessionID, checkpointID, hunkID string) (protocol.ChangeSet, error) {
	f.operation, f.sessionID, f.checkpointID, f.changeID = "hunk", sessionID, checkpointID, hunkID
	return f.changeSet, f.err
}

func (f *fakeRestoreController) RestoreFile(_ context.Context, sessionID, checkpointID, fileID string) (protocol.ChangeSet, error) {
	f.operation, f.sessionID, f.checkpointID, f.changeID = "file", sessionID, checkpointID, fileID
	return f.changeSet, f.err
}

func (f *fakeRestoreController) RestoreAll(_ context.Context, sessionID, checkpointID string) (protocol.ChangeSet, error) {
	f.operation, f.sessionID, f.checkpointID, f.changeID = "all", sessionID, checkpointID, ""
	return f.changeSet, f.err
}

var _ RestoreController = (*fakeRestoreController)(nil)

type fakeApprovalController struct {
	sessionID   string
	approvalID  string
	pendingOnly bool
	request     protocol.ApprovalDecisionRequest
	approval    protocol.CommandApproval
	err         error
}

func (f *fakeApprovalController) List(_ context.Context, sessionID string, pendingOnly bool) (protocol.ApprovalListResponse, error) {
	f.sessionID, f.pendingOnly = sessionID, pendingOnly
	return protocol.ApprovalListResponse{Approvals: []protocol.CommandApproval{f.approval}}, f.err
}

func (f *fakeApprovalController) Decide(_ context.Context, sessionID, approvalID string, request protocol.ApprovalDecisionRequest) (protocol.CommandApproval, error) {
	f.sessionID, f.approvalID, f.request = sessionID, approvalID, request
	return f.approval, f.err
}

var _ ApprovalController = (*fakeApprovalController)(nil)

func (f *fakeSessionCreator) CreateSession(_ context.Context, request protocol.CreateSessionRequest) (protocol.AgentSession, error) {
	f.request = request
	return f.created, f.err
}
