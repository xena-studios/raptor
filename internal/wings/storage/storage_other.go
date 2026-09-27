//go:build !linux

package storage

import (
	"context"
	"errors"
)

// Quotas and loop volumes need Linux, where Wings runs. These stubs keep the
// package building elsewhere (development on macOS).

var errLinuxOnly = errors.New("storage: Linux only")

// Usage is a project's current usage and limit.
type Usage struct {
	Bytes      int64
	Inodes     int64
	LimitBytes int64
}

// Mount is what's mounted at a path.
type Mount struct{ Source, FSType, Options string }

// HasProjectQuota is always false off Linux.
func (Mount) HasProjectQuota() bool { return false }

// FindMount isn't supported off Linux.
func FindMount(string) (Mount, bool, error) { return Mount{}, false, errLinuxOnly }

// Project isn't supported off Linux.
func Project(string) (uint32, error) { return 0, errLinuxOnly }

// ApplyProject isn't supported off Linux.
func ApplyProject(string, uint32) error { return errLinuxOnly }

// SetLimit isn't supported off Linux.
func SetLimit(string, uint32, int64) error { return errLinuxOnly }

// GetUsage isn't supported off Linux.
func GetUsage(string, uint32) (Usage, error) { return Usage{}, errLinuxOnly }

// EnableDirectIO isn't supported off Linux.
func EnableDirectIO(Mount) error { return errLinuxOnly }

// ImageSpec describes a tier 2 volume.
type ImageSpec struct {
	Image, Mountpoint, UnitDir string
	Size                       int64
}

// CreateImage isn't supported off Linux.
func CreateImage(context.Context, ImageSpec) error { return errLinuxOnly }

// Grow isn't supported off Linux.
func Grow(context.Context, ImageSpec, int64) error { return errLinuxOnly }

// DirectIO is always false off Linux.
func DirectIO(Mount) bool { return false }

// BackingFile is always "" off Linux.
func BackingFile(Mount) string { return "" }

// Space isn't supported off Linux.
func Space(string) (int64, int64, error) { return 0, 0, errLinuxOnly }
