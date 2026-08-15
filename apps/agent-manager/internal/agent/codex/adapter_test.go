package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/agent"
	"github.com/coco-papiyon/maatgen/apps/agent-manager/internal/protocol"
)

func TestCheckAndRunCodex(t *testing.T) {
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

	repository := t.TempDir()
	var outputs []agent.Output
	result, err := adapter.Run(context.Background(), agent.RunRequest{
		Directory: repository,
		Prompt:    "implement the task",
		Model:     "test-model",
		Timeout:   time.Second,
	}, func(output agent.Output) error {
		outputs = append(outputs, output)
		return nil
	})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("run result = %#v, err = %v", result, err)
	}
	arguments := emittedArguments(t, outputs)
	want := []string{
		"--ask-for-approval", "never", "--sandbox", "workspace-write", "--cd", repository,
		"--model", "test-model", "exec", "--json", "-",
	}
	if strings.Join(arguments, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("arguments = %#v, want %#v", arguments, want)
	}
	if !hasAgentOutput(outputs, agent.OutputStdout, "stdin=implement the task") ||
		!hasAgentOutput(outputs, agent.OutputStderr, "diagnostic") {
		t.Fatalf("outputs = %#v", outputs)
	}
}

func TestRunCodexResumeArguments(t *testing.T) {
	t.Setenv("MAATGEN_CODEX_HELPER", "1")
	adapter := New(os.Args[0])
	adapter.prefixArgs = []string{"-test.run=TestCodexHelper", "--"}
	repository := t.TempDir()
	var outputs []agent.Output
	_, err := adapter.Run(context.Background(), agent.RunRequest{
		Directory: repository, Prompt: "continue", ThreadID: "thread-123", Timeout: time.Second,
	}, func(output agent.Output) error {
		outputs = append(outputs, output)
		return nil
	})
	if err != nil {
		t.Fatalf("resume Codex: %v", err)
	}
	arguments := emittedArguments(t, outputs)
	joined := strings.Join(arguments, " ")
	if !strings.Contains(joined, "exec resume --json thread-123 -") || strings.Contains(joined, "--model") {
		t.Fatalf("resume arguments = %#v", arguments)
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
	encoded, _ := json.Marshal(arguments)
	input, _ := io.ReadAll(os.Stdin)
	fmt.Printf("args=%s\n", encoded)
	fmt.Printf("stdin=%s\n", input)
	fmt.Fprintln(os.Stderr, "diagnostic")
	os.Exit(0)
}

func emittedArguments(t *testing.T, outputs []agent.Output) []string {
	t.Helper()
	for _, output := range outputs {
		if output.Stream == agent.OutputStdout && strings.HasPrefix(output.Line, "args=") {
			var arguments []string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(output.Line, "args=")), &arguments); err != nil {
				t.Fatalf("decode arguments: %v", err)
			}
			return arguments
		}
	}
	t.Fatalf("arguments were not emitted: %#v", outputs)
	return nil
}

func hasAgentOutput(outputs []agent.Output, stream agent.OutputStream, text string) bool {
	for _, output := range outputs {
		if output.Stream == stream && strings.Contains(output.Line, text) {
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
