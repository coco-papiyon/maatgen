package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	ErrCanceled = errors.New("process canceled")
	ErrTimeout  = errors.New("process timed out")
)

type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"
)

type Output struct {
	Stream Stream
	Line   string
}

type Spec struct {
	Path    string
	Args    []string
	Dir     string
	Env     []string
	Stdin   string
	Timeout time.Duration
}

type Result struct {
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Canceled   bool
	TimedOut   bool
}

type Handler func(Output) error

type Runner struct{}

func (Runner) Run(ctx context.Context, spec Spec, handler Handler) (Result, error) {
	if spec.Path == "" {
		return Result{}, errors.New("process path is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{Canceled: true}, fmt.Errorf("%w: %v", ErrCanceled, err)
	}

	runCtx := ctx
	cancel := func() {}
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
	}
	defer cancel()

	command := exec.Command(spec.Path, spec.Args...)
	command.Dir = spec.Dir
	command.Stdin = strings.NewReader(spec.Stdin)
	if spec.Env != nil {
		command.Env = spec.Env
	} else {
		command.Env = os.Environ()
	}
	prepareCommand(command)

	stdout, err := command.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open stderr: %w", err)
	}

	result := Result{StartedAt: time.Now().UTC(), ExitCode: -1}
	if err := command.Start(); err != nil {
		return Result{}, fmt.Errorf("start process: %w", err)
	}
	contained, err := attachProcess(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return Result{}, fmt.Errorf("contain process: %w", err)
	}
	defer contained.Close()

	var handlerMu sync.Mutex
	stopOnce := sync.Once{}
	stop := func() {
		stopOnce.Do(func() { _ = contained.Terminate(command) })
	}
	scanErrors := make(chan error, 2)
	go scanOutput(stdout, Stdout, handler, &handlerMu, stop, scanErrors)
	go scanOutput(stderr, Stderr, handler, &handlerMu, stop, scanErrors)

	var contextErr error
	var scanErr error
	contextDone := runCtx.Done()
	for range 2 {
		select {
		case err := <-scanErrors:
			if err != nil && scanErr == nil {
				scanErr = err
			}
		case <-contextDone:
			contextErr = runCtx.Err()
			contextDone = nil
			stop()
			if err := <-scanErrors; err != nil && scanErr == nil {
				scanErr = err
			}
		}
	}
	waitErr := command.Wait()
	result.FinishedAt = time.Now().UTC()
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}

	if scanErr != nil {
		return result, scanErr
	}
	if contextErr != nil {
		if errors.Is(contextErr, context.DeadlineExceeded) && ctx.Err() == nil {
			result.TimedOut = true
			return result, fmt.Errorf("%w after %s", ErrTimeout, spec.Timeout)
		}
		result.Canceled = true
		return result, fmt.Errorf("%w: %v", ErrCanceled, contextErr)
	}
	var exitError *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitError) {
		return result, fmt.Errorf("wait for process: %w", waitErr)
	}
	return result, nil
}

func scanOutput(
	reader io.Reader,
	stream Stream,
	handler Handler,
	handlerMu *sync.Mutex,
	stop func(),
	result chan<- error,
) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		if handler == nil {
			continue
		}
		handlerMu.Lock()
		err := handler(Output{Stream: stream, Line: scanner.Text()})
		handlerMu.Unlock()
		if err != nil {
			stop()
			result <- fmt.Errorf("handle %s: %w", stream, err)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		stop()
		result <- fmt.Errorf("read %s: %w", stream, err)
		return
	}
	result <- nil
}
