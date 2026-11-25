//go:build unix

package cli

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on path, creating it with mode 0600, and
// blocks until no other process holds it. The kernel drops the lock when the
// process dies, so a crashed writer never leaves the config locked.
func lockFile(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
