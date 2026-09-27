//go:build !linux

package docker

// SeccompProfile is only built on Linux, where Wings runs.
func SeccompProfile() string { return "" }
