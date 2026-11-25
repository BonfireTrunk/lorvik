//go:build !unix

package cli

import (
	"os"
	"os/exec"
)

// There are no process groups here: only the process itself is killed or
// signalled.
func setProcessGroup(*exec.Cmd) {}

func killProcessGroup(c *exec.Cmd) error { return c.Process.Kill() }

func signalProcessGroup(c *exec.Cmd, sig os.Signal) error {
	if err := c.Process.Signal(sig); err != nil {
		return c.Process.Kill()
	}
	return nil
}
