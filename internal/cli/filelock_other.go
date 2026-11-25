//go:build !unix

package cli

// lockFile is a no-op where flock does not exist; releases ship for unix only,
// so concurrent config writers are not serialised on other systems.
func lockFile(string) (unlock func(), err error) { return func() {}, nil }
