package cmdutil

import (
	"io"
	"time"

	"github.com/BonfireTrunk/lorvik/internal/client"
)

// Factory carries the dependencies every command may need. Commands take it
// at construction and resolve expensive things (the API client) lazily in
// RunE so --help and tests stay cheap.
type Factory struct {
	IOStreams *IOStreams
	// Client, when set, replaces the default client construction from flags,
	// environment and config files. Tests use it to point at httptest.
	Client func() (*client.Client, error)
	// Env looks up an environment variable. Nil means os.Getenv.
	Env func(string) string
	// Getwd returns the working directory. Nil means os.Getwd.
	Getwd func() (string, error)
	// Now returns the current time. Nil means time.Now.
	Now func() time.Time
	// Hostname returns the machine name. Nil means os.Hostname.
	Hostname func() (string, error)
	// Getppid returns the CLI's parent pid. Nil means os.Getppid.
	Getppid func() int
	// Rand supplies random bytes for generated session labels. Nil means
	// crypto/rand.
	Rand io.Reader
	// Proc looks a process up by pid for the session label's process walk.
	// Nil means the operating system's process table; tests pass a fake.
	Proc func(pid int) (ProcInfo, error)
	// Prompter asks a person at a terminal. Nil means the terminal prompter
	// on IOStreams; tests pass a scripted fake.
	Prompter Prompter
	// LockPoll and LockRenew replace the 5 second wait poll and the ttl/2
	// renewal period of the lock commands. Zero means the defaults; tests set
	// them so they need not sleep.
	LockPoll  time.Duration
	LockRenew time.Duration
	// GitHubURL replaces https://github.com as the host of the device flow
	// endpoints. Empty means GitHub; tests point it at httptest.
	GitHubURL string
	// Sleep waits between device-flow polls. Nil means a context-aware
	// time.Sleep; tests pass a recorder.
	Sleep func(time.Duration)
}

// ProcInfo describes one process: executable name, parent pid, and an opaque
// start-time string that together with the pid identifies it across pid reuse.
type ProcInfo struct {
	Name  string
	PPID  int
	Start string
}

// Time returns the current time from Now, or the wall clock when Now is nil.
func (f *Factory) Time() time.Time {
	if f.Now == nil {
		return time.Now()
	}
	return f.Now()
}
