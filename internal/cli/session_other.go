//go:build !linux && !darwin && !windows

package cli

import (
	"errors"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// lookupProc has no process table here; the label falls back to the CLI's
// parent pid and hostname.
func lookupProc(int) (cmdutil.ProcInfo, error) {
	return cmdutil.ProcInfo{}, errors.New("process lookup is not supported on this platform")
}
