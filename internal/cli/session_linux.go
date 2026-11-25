//go:build linux

package cli

import (
	"fmt"
	"os"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

// lookupProc reads /proc/<pid>/stat.
func lookupProc(pid int) (cmdutil.ProcInfo, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return cmdutil.ProcInfo{}, err
	}
	return parseLinuxStat(b)
}
