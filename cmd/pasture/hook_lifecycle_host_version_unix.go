//go:build unix

package main

import (
	"errors"
	"os/exec"
	"syscall"
)

func prepareHostVersionProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopHostVersionProcess(command *exec.Cmd) error {
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
