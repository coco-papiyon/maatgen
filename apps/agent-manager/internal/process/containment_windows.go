//go:build windows

package process

import (
	"os/exec"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processContainment struct {
	job windows.Handle
}

func prepareCommand(_ *exec.Cmd) {}

func attachProcess(command *exec.Cmd) (*processContainment, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &processContainment{}, nil
	}
	contained := &processContainment{job: job}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = contained.Close()
		return &processContainment{}, nil
	}
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(command.Process.Pid),
	)
	if err != nil {
		_ = contained.Close()
		return &processContainment{}, nil
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = contained.Close()
		return &processContainment{}, nil
	}
	return contained, nil
}

func (c *processContainment) Terminate(command *exec.Cmd) error {
	if c.job != 0 {
		if err := windows.TerminateJobObject(c.job, 1); err == nil {
			return nil
		}
	}
	if command.Process == nil {
		return nil
	}
	// A parent process can forbid nested Job Objects. taskkill still terminates
	// the process tree without invoking a command shell in that environment.
	if err := exec.Command(
		"taskkill.exe", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F",
	).Run(); err == nil {
		return nil
	}
	return command.Process.Kill()
}

func (c *processContainment) Close() error {
	if c.job == 0 {
		return nil
	}
	err := windows.CloseHandle(c.job)
	c.job = 0
	return err
}
