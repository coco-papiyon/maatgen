package claude

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

func TestCheckAndRunClaude(t *testing.T) {
	t.Setenv("MAATGEN_CLAUDE_HELPER", "1")
	adapter := New(os.Args[0])
	adapter.prefixArgs = []string{"-test.run=TestClaudeHelper", "--"}
	info, err := adapter.Check(context.Background())
	if err != nil {
		t.Fatalf("check Claude Code: %v", err)
	}
	if info.Name != protocol.AgentClaude || info.Version != "2.1.235 (Claude Code)" || !filepath.IsAbs(info.Path) {
		t.Fatalf("info = %#v", info)
	}

	repository := t.TempDir()
	var outputs []agent.Output
	result, err := adapter.Run(context.Background(), agent.RunRequest{
		Directory: repository, Prompt: "implement the task", ThreadID: "01J-abc", Model: "claude-opus-5", Timeout: 5 * time.Second,
	}, func(output agent.Output) error { outputs = append(outputs, output); return nil })
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("run = %#v, err = %v", result, err)
	}
	joined := strings.Join(emittedArguments(t, outputs), " ")
	for _, expected := range []string{
		"--print", "--output-format stream-json", "--verbose",
		"--permission-mode bypassPermissions", "--model claude-opus-5", "--resume 01J-abc",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("arguments %q do not contain %q", joined, expected)
		}
	}
	// The prompt must reach the CLI on stdin rather than the argument list.
	if strings.Contains(joined, "implement the task") {
		t.Fatalf("prompt leaked into arguments: %q", joined)
	}
	if !hasOutput(outputs, agent.OutputStdout, `stdin=implement the task`) {
		t.Fatalf("outputs = %#v", outputs)
	}
	if !hasOutput(outputs, agent.OutputStderr, "diagnostic") {
		t.Fatalf("outputs = %#v", outputs)
	}
}

func TestRunWithoutModelOrThreadOmitsFlags(t *testing.T) {
	t.Setenv("MAATGEN_CLAUDE_HELPER", "1")
	adapter := New(os.Args[0])
	adapter.prefixArgs = []string{"-test.run=TestClaudeHelper", "--"}
	var outputs []agent.Output
	if _, err := adapter.Run(context.Background(), agent.RunRequest{
		Directory: t.TempDir(), Prompt: "first run", Timeout: 5 * time.Second,
	}, func(output agent.Output) error { outputs = append(outputs, output); return nil }); err != nil {
		t.Fatalf("run: %v", err)
	}
	joined := strings.Join(emittedArguments(t, outputs), " ")
	if strings.Contains(joined, "--model") || strings.Contains(joined, "--resume") {
		t.Fatalf("arguments = %q", joined)
	}
}

func TestRunRejectsEmptyPrompt(t *testing.T) {
	if _, err := New("claude").Run(context.Background(), agent.RunRequest{Directory: t.TempDir(), Prompt: "  "}, nil); err == nil {
		t.Fatal("expected an error for an empty prompt")
	}
}

func TestClaudeHelper(t *testing.T) {
	if os.Getenv("MAATGEN_CLAUDE_HELPER") != "1" {
		return
	}
	arguments := afterSeparator(os.Args)
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Println("2.1.235 (Claude Code)")
		os.Exit(0)
	}
	encoded, _ := json.Marshal(arguments)
	fmt.Printf("args=%s\n", encoded)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Printf("stdin=%s\n", scanner.Text())
	}
	fmt.Fprintln(os.Stderr, "diagnostic")
	os.Exit(0)
}

func emittedArguments(t *testing.T, outputs []agent.Output) []string {
	t.Helper()
	for _, output := range outputs {
		if output.Stream == agent.OutputStdout && strings.HasPrefix(output.Line, "args=") {
			var arguments []string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(output.Line, "args=")), &arguments); err != nil {
				t.Fatal(err)
			}
			return arguments
		}
	}
	t.Fatalf("arguments not emitted: %#v", outputs)
	return nil
}

func hasOutput(outputs []agent.Output, stream agent.OutputStream, value string) bool {
	for _, output := range outputs {
		if output.Stream == stream && strings.Contains(output.Line, value) {
			return true
		}
	}
	return false
}

func afterSeparator(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[index+1:]
		}
	}
	return nil
}
