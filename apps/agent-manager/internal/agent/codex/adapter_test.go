package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestCheckCodex(t *testing.T) {
	t.Setenv("MAATGEN_CODEX_HELPER", "1")
	adapter := New(os.Args[0])
	adapter.prefixArgs = []string{"-test.run=TestCodexHelper", "--"}

	info, err := adapter.Check(context.Background())
	if err != nil {
		t.Fatalf("check Codex: %v", err)
	}
	if info.Name != protocol.AgentCodex || info.Version != "codex-cli 1.2.3" || !filepath.IsAbs(info.Path) {
		t.Fatalf("info = %#v", info)
	}
}

func TestRunCodexRequiresApproval(t *testing.T) {
	t.Setenv("MAATGEN_CODEX_HELPER", "1")
	adapter := New(os.Args[0])
	adapter.prefixArgs = []string{"-test.run=TestCodexHelper", "--"}

	_, err := adapter.Run(context.Background(), agent.RunRequest{
		Directory: t.TempDir(), Prompt: "implement the task", Timeout: 5 * time.Second,
	}, func(agent.Output) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "requires a command approval handler") {
		t.Fatalf("err = %v, want approval-required error", err)
	}
}

func TestRunAppServerApproval(t *testing.T) {
	for _, decision := range []struct {
		name     string
		approved bool
		wantErr  bool
	}{
		{name: "approved", approved: true, wantErr: false},
		{name: "declined", approved: false, wantErr: true},
	} {
		t.Run(decision.name, func(t *testing.T) {
			t.Setenv("MAATGEN_CODEX_HELPER", "1")
			adapter := New(os.Args[0])
			adapter.prefixArgs = []string{"-test.run=TestCodexHelper", "--"}

			var received agent.ApprovalRequest
			approval := func(_ context.Context, request agent.ApprovalRequest) (agent.ApprovalDecision, error) {
				received = request
				return agent.ApprovalDecision{Approved: decision.approved}, nil
			}

			result, err := adapter.Run(context.Background(), agent.RunRequest{
				Directory: t.TempDir(), Prompt: "implement the task", Model: "test-model",
				Timeout: 10 * time.Second, Approval: approval,
			}, func(agent.Output) error { return nil })

			if decision.wantErr {
				if err == nil {
					t.Fatalf("expected an error for a declined command, got result = %#v", result)
				}
			} else if err != nil {
				t.Fatalf("run app-server: %v", err)
			}
			if received.Command != "rm -rf /tmp/example" {
				t.Fatalf("approval request command = %q", received.Command)
			}
			if received.WorkingDirectory != "/workspace" {
				t.Fatalf("approval request working directory = %q", received.WorkingDirectory)
			}
		})
	}
}

func TestRunAppServerAutoApprove(t *testing.T) {
	t.Setenv("MAATGEN_CODEX_HELPER", "1")
	adapter := New(os.Args[0])
	adapter.prefixArgs = []string{"-test.run=TestCodexHelper", "--"}

	approvalCalled := false
	approval := func(_ context.Context, request agent.ApprovalRequest) (agent.ApprovalDecision, error) {
		approvalCalled = true
		return agent.ApprovalDecision{Approved: false}, nil
	}

	_, err := adapter.Run(context.Background(), agent.RunRequest{
		Directory: t.TempDir(), Prompt: "implement the task", Model: "test-model",
		Timeout: 10 * time.Second, Approval: approval, AutoApprove: true,
	}, func(agent.Output) error { return nil })

	if err != nil {
		t.Fatalf("run app-server: %v", err)
	}
	if approvalCalled {
		t.Fatal("approval handler was called even though AutoApprove was set")
	}
}

func TestGetUsageCodex(t *testing.T) {
	t.Setenv("MAATGEN_CODEX_HELPER", "1")
	adapter := New(os.Args[0])
	adapter.prefixArgs = []string{"-test.run=TestCodexHelper", "--"}

	usage, err := adapter.GetUsage(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("get Codex usage: %v", err)
	}
	if len(usage.Windows) != 2 || usage.Windows[0].Name != "primary" || usage.Windows[0].UsedPercent != 25 || usage.Windows[0].RemainingPercent != 75 || usage.Windows[1].Name != "secondary" || usage.Windows[1].UsedPercent != 60 || usage.Windows[1].RemainingPercent != 40 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestCodexHelper(t *testing.T) {
	if os.Getenv("MAATGEN_CODEX_HELPER") != "1" {
		return
	}
	arguments := afterSeparator(os.Args)
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Println("codex-cli 1.2.3")
		os.Exit(0)
	}
	if len(arguments) == 2 && arguments[0] == "app-server" && arguments[1] == "--stdio" {
		runFakeAppServer()
		os.Exit(0)
	}
	os.Exit(1)
}

// runFakeAppServer speaks just enough of the Codex app-server JSON-RPC
// protocol to drive one turn through a single command-execution approval
// request, so adapter tests can exercise runAppServer without a real Codex
// binary.
func runFakeAppServer() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	write := func(value any) {
		data, _ := json.Marshal(value)
		os.Stdout.Write(append(data, '\n'))
	}
	const turnID = "turn-1"
	for scanner.Scan() {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) != nil {
			continue
		}
		switch message.Method {
		case "initialize":
			write(map[string]any{"id": message.ID, "result": map[string]any{}})
		case "initialized":
			// notification, no response
		case "account/rateLimits/read":
			write(map[string]any{"id": message.ID, "result": map[string]any{
				"rateLimits": map[string]any{
					"primary":   map[string]any{"usedPercent": 25, "resetsAt": 4102444800},
					"secondary": map[string]any{"usedPercent": 60, "resetsAt": 4102444800},
				},
			}})
		case "thread/start":
			write(map[string]any{"id": message.ID, "result": map[string]any{
				"model": "test-model", "thread": map[string]any{"id": "thread-abc"},
			}})
		case "turn/start":
			write(map[string]any{"id": message.ID, "result": map[string]any{"turn": map[string]any{"id": turnID}}})
			write(map[string]any{
				"id": 100, "method": "item/commandExecution/requestApproval",
				"params": map[string]any{"command": "rm -rf /tmp/example", "cwd": "/workspace", "reason": "cleanup"},
			})
		case "":
			var id int
			if json.Unmarshal(message.ID, &id) != nil || id != 100 {
				continue
			}
			var decision struct {
				Decision string `json:"decision"`
			}
			_ = json.Unmarshal(message.Result, &decision)
			if decision.Decision == "accept" {
				write(map[string]any{"method": "turn/completed", "params": map[string]any{
					"turn": map[string]any{"id": turnID, "status": "completed"},
				}})
			} else {
				write(map[string]any{"method": "turn/completed", "params": map[string]any{
					"turn": map[string]any{"id": turnID, "status": "failed", "error": map[string]any{"message": "declined by approval"}},
				}})
			}
			return
		}
	}
}

func afterSeparator(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[index+1:]
		}
	}
	return nil
}
