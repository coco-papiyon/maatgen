package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	managerprocess "github.com/coco-papiyon/maatgen/apps/agent-manager/internal/process"
)

type appServerMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type appServerConnection struct {
	ctx         context.Context
	cancel      context.CancelFunc
	cmd         *exec.Cmd
	contained   *managerprocess.Containment
	stdin       io.WriteCloser
	emit        agent.Emitter
	emitMu      sync.Mutex
	approval    agent.ApprovalHandler
	request     agent.RunRequest
	nextID      atomic.Int64
	writeMu     sync.Mutex
	pendingMu   sync.Mutex
	pending     map[int64]chan appServerMessage
	events      chan appServerMessage
	done        chan struct{}
	errMu       sync.Mutex
	processErr  error
	actualModel string
}

func (a *Adapter) runAppServer(ctx context.Context, binary string, prefixArgs []string, directory string, request agent.RunRequest, emit agent.Emitter) (agent.RunResult, error) {
	startedAt := time.Now().UTC()
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection, err := startAppServer(runCtx, binary, prefixArgs, directory, request, emit)
	if err != nil {
		return agent.RunResult{ExitCode: -1, StartedAt: startedAt, FinishedAt: time.Now().UTC()}, err
	}
	defer connection.close()
	err = connection.runTurn(runCtx, request.Prompt, request.Model)
	finishedAt := time.Now().UTC()
	result := agent.RunResult{ExitCode: 0, StartedAt: startedAt, FinishedAt: finishedAt, ActualModel: connection.actualModel}
	if err != nil {
		result.ExitCode = 1
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
		}
		if errors.Is(runCtx.Err(), context.Canceled) && !result.TimedOut {
			result.Canceled = true
		}
	}
	return result, err
}

func startAppServer(ctx context.Context, binary string, prefixArgs []string, directory string, request agent.RunRequest, emit agent.Emitter) (*appServerConnection, error) {
	processCtx, cancel := context.WithCancel(ctx)
	args := append(append([]string{}, prefixArgs...), "app-server", "--stdio")
	cmd := exec.Command(binary, args...)
	cmd.Dir = directory
	managerprocess.PrepareInteractive(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start Codex app-server: %w", err)
	}
	contained, err := managerprocess.AttachInteractive(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		return nil, fmt.Errorf("contain Codex app-server: %w", err)
	}
	c := &appServerConnection{
		ctx: processCtx, cancel: cancel, cmd: cmd, contained: contained, stdin: stdin, emit: emit,
		approval: request.Approval, request: request, pending: map[int64]chan appServerMessage{},
		events: make(chan appServerMessage, 256), done: make(chan struct{}),
	}
	go c.read(stdout)
	go c.readStderr(stderr)
	go func() {
		<-processCtx.Done()
		_ = contained.Terminate(cmd)
	}()
	go func() {
		err := cmd.Wait()
		_ = contained.Close()
		c.errMu.Lock()
		c.processErr = err
		c.errMu.Unlock()
		close(c.done)
	}()
	var initialized json.RawMessage
	if err := c.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "maatgen", "version": "0.1.0"}, "capabilities": map[string]bool{"experimentalApi": true}}, &initialized); err != nil {
		c.close()
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.close()
		return nil, err
	}
	var thread struct {
		Model  string `json:"model"`
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	method := "thread/start"
	params := map[string]any{"cwd": directory, "model": emptyNil(request.Model), "sandbox": "workspace-write", "approvalPolicy": "on-request", "ephemeral": false}
	if request.ThreadID != "" {
		method = "thread/resume"
		params["threadId"] = request.ThreadID
	}
	if err := c.call(ctx, method, params, &thread); err != nil {
		c.close()
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if thread.Thread.ID == "" {
		thread.Thread.ID = request.ThreadID
	}
	if thread.Thread.ID == "" {
		c.close()
		return nil, errors.New("Codex app-server did not return a thread id")
	}
	c.request.ThreadID = thread.Thread.ID
	c.actualModel = thread.Model
	return c, nil
}

func (c *appServerConnection) runTurn(ctx context.Context, prompt, model string) error {
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	params := map[string]any{"threadId": c.request.ThreadID, "input": []map[string]string{{"type": "text", "text": prompt}}, "model": emptyNil(model), "effort": emptyNil(c.request.ReasoningEffort)}
	// thread/start emits thread/started before turn/start. The parser persists it,
	// and the app-server accepts the returned thread id from the response. If this
	// is a new thread, use the ID observed by the run service on that event.
	if params["threadId"] == "" {
		delete(params, "threadId")
	}
	if err := c.call(ctx, "turn/start", params, &started); err != nil {
		// Recent app-server versions require threadId. Recover it from the latest
		// thread/started event cached by read() and retry once.
		if c.request.ThreadID == "" {
			return fmt.Errorf("start Codex turn: %w", err)
		}
		return err
	}
	turnID := started.Turn.ID
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return c.processError()
		case event := <-c.events:
			if event.Method != "turn/completed" {
				continue
			}
			var params struct {
				Turn struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
			}
			if json.Unmarshal(event.Params, &params) != nil || (turnID != "" && params.Turn.ID != turnID) {
				continue
			}
			if params.Turn.Status != "completed" {
				message := params.Turn.Status
				if params.Turn.Error != nil {
					message = params.Turn.Error.Message
				}
				return errors.New(message)
			}
			return nil
		}
	}
}

func (c *appServerConnection) call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)
	response := make(chan appServerMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = response
	c.pendingMu.Unlock()
	defer func() { c.pendingMu.Lock(); delete(c.pending, id); c.pendingMu.Unlock() }()
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.processError()
	case message := <-response:
		if message.Error != nil {
			return fmt.Errorf("%s (%d)", message.Error.Message, message.Error.Code)
		}
		if result != nil && len(message.Result) > 0 {
			if err := json.Unmarshal(message.Result, result); err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *appServerConnection) notify(method string, params any) error {
	return c.write(map[string]any{"method": method, "params": params})
}
func (c *appServerConnection) write(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *appServerConnection) read(source io.Reader) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if err := c.emitOutput(agent.Output{Stream: agent.OutputStdout, Line: line}); err != nil {
			c.cancel()
			return
		}
		var message appServerMessage
		if json.Unmarshal([]byte(line), &message) != nil {
			continue
		}
		if message.Method != "" && len(message.ID) > 0 {
			go c.handleRequest(message)
			continue
		}
		if message.Method != "" {
			select {
			case c.events <- message:
			case <-c.ctx.Done():
				return
			}
			continue
		}
		var id int64
		if json.Unmarshal(message.ID, &id) != nil {
			continue
		}
		c.pendingMu.Lock()
		response := c.pending[id]
		c.pendingMu.Unlock()
		if response != nil {
			select {
			case response <- message:
			case <-c.ctx.Done():
				return
			}
		}
	}
}

func (c *appServerConnection) readStderr(source io.Reader) {
	scanner := bufio.NewScanner(source)
	for scanner.Scan() {
		_ = c.emitOutput(agent.Output{Stream: agent.OutputStderr, Line: scanner.Text()})
	}
}

func (c *appServerConnection) emitOutput(output agent.Output) error {
	if c.emit == nil {
		return nil
	}
	c.emitMu.Lock()
	defer c.emitMu.Unlock()
	return c.emit(output)
}

func (c *appServerConnection) handleRequest(message appServerMessage) {
	result := any(nil)
	var requestErr error
	if message.Method == "item/fileChange/requestApproval" {
		// Working Tree edits are immediately visible by product design and are
		// reverted through checkpoints, not an Accept/Reject workflow.
		result = map[string]string{"decision": "accept"}
	} else if message.Method != "item/commandExecution/requestApproval" || c.approval == nil {
		requestErr = fmt.Errorf("unsupported Codex request %s", message.Method)
	} else {
		command, workingDirectory, explanation := appServerApprovalCommand(message.Params)
		if command == "" {
			requestErr = errors.New("Codex approval request did not include a command")
		} else {
			decision, err := c.approval(c.ctx, agent.ApprovalRequest{ProviderRequestID: string(message.ID), Command: command, Shell: currentShell(), WorkingDirectory: nonEmptyString(workingDirectory, c.request.Directory), Explanation: explanation})
			if err != nil {
				requestErr = err
			} else if decision.Approved {
				result = map[string]string{"decision": "accept"}
			} else {
				result = map[string]string{"decision": "decline"}
			}
		}
	}
	response := map[string]any{"id": json.RawMessage(message.ID)}
	if requestErr != nil {
		response["error"] = map[string]any{"code": -32603, "message": requestErr.Error()}
	} else {
		response["result"] = result
	}
	_ = c.write(response)
}

func appServerApprovalCommand(params json.RawMessage) (string, string, string) {
	var request struct {
		Command        string `json:"command"`
		CWD            string `json:"cwd"`
		Reason         string `json:"reason"`
		CommandActions []struct {
			Command string `json:"command"`
		} `json:"commandActions"`
		Proposed []string `json:"proposedExecpolicyAmendment"`
	}
	_ = json.Unmarshal(params, &request)
	if strings.TrimSpace(request.Command) != "" {
		return request.Command, request.CWD, request.Reason
	}
	var actions []string
	for _, action := range request.CommandActions {
		if strings.TrimSpace(action.Command) != "" {
			actions = append(actions, action.Command)
		}
	}
	if len(actions) > 0 {
		return strings.Join(actions, " ; "), request.CWD, request.Reason
	}
	if len(request.Proposed) > 0 {
		return strings.Join(request.Proposed, " "), request.CWD, request.Reason
	}
	return "", request.CWD, request.Reason
}

func (c *appServerConnection) close() { c.cancel(); _ = c.stdin.Close(); <-c.done }
func (c *appServerConnection) processError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	if c.processErr == nil {
		return errors.New("Codex app-server stopped")
	}
	return c.processErr
}
func emptyNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
func currentShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "sh"
}

func nonEmptyString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
