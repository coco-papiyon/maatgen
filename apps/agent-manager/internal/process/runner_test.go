package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunnerStreamsInputOutputAndExitCode(t *testing.T) {
	t.Setenv("MAATGEN_PROCESS_HELPER", "1")
	var outputs []Output
	result, err := (Runner{}).Run(context.Background(), helperSpec("echo"), func(output Output) error {
		outputs = append(outputs, output)
		return nil
	})
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if !containsOutput(outputs, Stdout, "stdin=hello") || !containsOutput(outputs, Stderr, "diagnostic") {
		t.Fatalf("outputs = %#v", outputs)
	}
}

func TestRunnerTimeoutAndCancel(t *testing.T) {
	t.Setenv("MAATGEN_PROCESS_HELPER", "1")
	timeoutSpec := helperSpec("sleep")
	timeoutSpec.Timeout = 50 * time.Millisecond
	timedOut, err := (Runner{}).Run(context.Background(), timeoutSpec, nil)
	if !errors.Is(err, ErrTimeout) || !timedOut.TimedOut {
		t.Fatalf("timeout result = %#v, err = %v", timedOut, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled, err := (Runner{}).Run(ctx, helperSpec("sleep"), nil)
	if !errors.Is(err, ErrCanceled) || !canceled.Canceled {
		t.Fatalf("cancel result = %#v, err = %v", canceled, err)
	}
}

func TestRunnerStopsWhenOutputHandlerFails(t *testing.T) {
	t.Setenv("MAATGEN_PROCESS_HELPER", "1")
	handlerErr := errors.New("event sink unavailable")
	startedAt := time.Now()
	_, err := (Runner{}).Run(context.Background(), helperSpec("stream-then-sleep"), func(Output) error {
		return handlerErr
	})
	if !errors.Is(err, handlerErr) {
		t.Fatalf("handler error = %v", err)
	}
	if time.Since(startedAt) > 2*time.Second {
		t.Fatal("process was not stopped after handler failure")
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("MAATGEN_PROCESS_HELPER") != "1" {
		return
	}
	arguments := argumentsAfterSeparator(os.Args)
	if len(arguments) == 0 {
		os.Exit(2)
	}
	switch arguments[0] {
	case "echo":
		input, _ := io.ReadAll(os.Stdin)
		fmt.Printf("stdin=%s\n", input)
		fmt.Fprintln(os.Stderr, "diagnostic")
		os.Exit(7)
	case "sleep":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "stream-then-sleep":
		fmt.Println("event")
		time.Sleep(10 * time.Second)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func helperSpec(mode string) Spec {
	return Spec{
		Path:  os.Args[0],
		Args:  []string{"-test.run=TestProcessHelper", "--", mode},
		Env:   os.Environ(),
		Stdin: "hello",
	}
}

func argumentsAfterSeparator(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[index+1:]
		}
	}
	return nil
}

func containsOutput(outputs []Output, stream Stream, text string) bool {
	for _, output := range outputs {
		if output.Stream == stream && strings.Contains(output.Line, text) {
			return true
		}
	}
	return false
}
