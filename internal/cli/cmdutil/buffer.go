package cmdutil

import "bytes"

// Buffer is a bytes.Buffer that tests can read back as a string.
type Buffer struct{ bytes.Buffer }
