//go:build unix

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own group so a kill or a forwarded
// signal reaches the grandchildren a shell command starts too.
func setProcessGroup(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func killProcessGroup(c *exec.Cmd) error {
	return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
}

func signalProcessGroup(c *exec.Cmd, sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return c.Process.Signal(sig)
	}
	return syscall.Kill(-c.Process.Pid, s)
}
