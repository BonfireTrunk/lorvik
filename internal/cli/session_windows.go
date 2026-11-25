//go:build windows

package cli

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/BonfireTrunk/lorvik/internal/cli/cmdutil"
)

const processQueryLimitedInformation = 0x1000

// lookupProc finds pid in a Toolhelp snapshot for its name and parent, and
// reads its creation time with GetProcessTimes.
func lookupProc(pid int) (cmdutil.ProcInfo, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return cmdutil.ProcInfo{}, err
	}
	defer syscall.CloseHandle(snap)
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := syscall.Process32First(snap, &e); err != nil {
		return cmdutil.ProcInfo{}, err
	}
	for {
		if int(e.ProcessID) == pid {
			info := cmdutil.ProcInfo{Name: syscall.UTF16ToString(e.ExeFile[:]), PPID: int(e.ParentProcessID)}
			start, err := processStart(uint32(pid))
			if err != nil {
				return cmdutil.ProcInfo{}, err
			}
			info.Start = start
			return info, nil
		}
		if err := syscall.Process32Next(snap, &e); err != nil {
			if errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
				return cmdutil.ProcInfo{}, fmt.Errorf("process %d not found", pid)
			}
			return cmdutil.ProcInfo{}, err
		}
	}
}

func processStart(pid uint32) (string, error) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d", uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime)), nil
}
