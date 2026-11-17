//go:build !linux

package files

import "os"

// clone is Linux only; elsewhere files are always copied.
func clone(_, _ *os.File) bool { return false }
