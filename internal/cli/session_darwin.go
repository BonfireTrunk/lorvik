//go:build darwin

package cli

import (
	"context"
	"os/exec"
	"strconv"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// lookupProc asks ps for the parent pid, start time and command of pid.
func lookupProc(pid int) (cmdutil.ProcInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "ppid=,lstart=,comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return cmdutil.ProcInfo{}, err
	}
	return parsePS(string(out))
}
