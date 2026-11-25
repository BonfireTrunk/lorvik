package domain

import "fmt"

// errorf is fmt.Errorf; kept local so the domain package's wrapping style is
// visible in one place.
func errorf(format string, a ...any) error { return fmt.Errorf(format, a...) }
