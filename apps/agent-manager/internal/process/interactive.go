package process

import "os/exec"

// Containment owns the platform-specific process-tree boundary for an
// interactive process whose stdin and stdout are managed by the caller.
type Containment struct {
	target interactiveContainment
}

type interactiveContainment interface {
	Terminate(*exec.Cmd) error
	Close() error
}

// PrepareInteractive configures process-group creation before command.Start.
func PrepareInteractive(command *exec.Cmd) {
	prepareCommand(command)
}

// AttachInteractive binds a started command to its platform containment.
func AttachInteractive(command *exec.Cmd) (*Containment, error) {
	target, err := attachProcess(command)
	if err != nil {
		return nil, err
	}
	return &Containment{target: target}, nil
}

// Terminate stops the command and its contained child process tree.
func (containment *Containment) Terminate(command *exec.Cmd) error {
	return containment.target.Terminate(command)
}

func (containment *Containment) Close() error {
	return containment.target.Close()
}
