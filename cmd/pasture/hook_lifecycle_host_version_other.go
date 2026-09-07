//go:build !unix

package main

import (
	"errors"
	"os"
	"os/exec"
)

func prepareHostVersionProcess(command *exec.Cmd) {}

// Outside Unix only the direct process is terminated. Owned pipe reads still
// close on cancellation, so inherited writers cannot extend the query budget.
func stopHostVersionProcess(command *exec.Cmd) error {
	err := command.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
