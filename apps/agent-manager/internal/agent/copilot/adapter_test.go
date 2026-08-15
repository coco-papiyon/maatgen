package copilot

import (
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

func TestCheckAndRunCopilot(t *testing.T) {
	t.Setenv("MAATGEN_COPILOT_HELPER", "1")
	adapter := New(os.Args[0])
	adapter.prefixArgs = []string{"-test.run=TestCopilotHelper", "--"}
	info, err := adapter.Check(context.Background())
	if err != nil { t.Fatalf("check Copilot: %v", err) }
	if info.Name != protocol.AgentCopilot || info.Version != "GitHub Copilot CLI 1.0.51" || !filepath.IsAbs(info.Path) {
		t.Fatalf("info = %#v", info)
	}

	repository := t.TempDir()
	var outputs []agent.Output
	result, err := adapter.Run(context.Background(), agent.RunRequest{
		Directory: repository, Prompt: "implement the task", ThreadID: "session-123", Model: "gpt-5.4", Timeout: time.Second,
	}, func(output agent.Output) error { outputs = append(outputs, output); return nil })
	if err != nil || result.ExitCode != 0 { t.Fatalf("run = %#v, err = %v", result, err) }
	arguments := emittedArguments(t, outputs)
	joined := strings.Join(arguments, " ")
	for _, expected := range []string{"-C " + repository, "--prompt implement the task", "--output-format json", "--allow-all", "--no-ask-user", "--resume=session-123", "--model gpt-5.4"} {
		if !strings.Contains(joined, expected) { t.Fatalf("arguments %q do not contain %q", joined, expected) }
	}
	if !hasOutput(outputs, agent.OutputStderr, "diagnostic") { t.Fatalf("outputs = %#v", outputs) }
}

func TestCopilotHelper(t *testing.T) {
	if os.Getenv("MAATGEN_COPILOT_HELPER") != "1" { return }
	arguments := afterSeparator(os.Args)
	if len(arguments) == 1 && arguments[0] == "--version" {
		fmt.Println("GitHub Copilot CLI 1.0.51")
		os.Exit(0)
	}
	encoded, _ := json.Marshal(arguments)
	fmt.Printf("args=%s\n", encoded)
	fmt.Fprintln(os.Stderr, "diagnostic")
	os.Exit(0)
}

func emittedArguments(t *testing.T, outputs []agent.Output) []string {
	t.Helper()
	for _, output := range outputs {
		if output.Stream == agent.OutputStdout && strings.HasPrefix(output.Line, "args=") {
			var arguments []string
			if err := json.Unmarshal([]byte(strings.TrimPrefix(output.Line, "args=")), &arguments); err != nil { t.Fatal(err) }
			return arguments
		}
	}
	t.Fatalf("arguments not emitted: %#v", outputs)
	return nil
}

func hasOutput(outputs []agent.Output, stream agent.OutputStream, value string) bool {
	for _, output := range outputs { if output.Stream == stream && strings.Contains(output.Line, value) { return true } }
	return false
}

func afterSeparator(arguments []string) []string {
	for index, argument := range arguments { if argument == "--" { return arguments[index+1:] } }
	return nil
}
