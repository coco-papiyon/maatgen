//go:build linux || darwin

package process

import (
	"errors"
	"os/exec"
	"syscall"
)

type processContainment struct{}

func prepareCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProcess(_ *exec.Cmd) (*processContainment, error) {
	return &processContainment{}, nil
}

func (*processContainment) Terminate(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (*processContainment) Close() error { return nil }
