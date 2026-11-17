// Package demo is what the Panel and the public demo's node agree on about
// sandboxes (#449, docs/PANEL.md#sandboxes): their limits, their lifetime,
// and which library eggs fit. Wings enforces every one of them on the demo
// node; the Panel uses the same numbers so it never asks for what Wings
// would refuse.
package demo

import "time"

// A sandbox's limits: one core, 1.5 GB, and a 10 GB disk.
const (
	MemoryMiB  = 1536
	CPUPercent = 100
	DiskMiB    = 10 << 10
)

// Lifetimes. A sandbox lasts Lifetime from when its install finished (a
// slow install doesn't eat it), once extended by Extension; one whose
// install takes longer than InstallLimit is deleted unfinished.
const (
	Lifetime     = 15 * time.Minute
	Extension    = 15 * time.Minute
	InstallLimit = 30 * time.Minute
)

// Defaults a demo node starts with (`raptor demo on` changes them).
const (
	// DefaultMaxSandboxes is how many run at once: four at 1.5 GB on an
	// 8 GB node leaves room for the showcase servers.
	DefaultMaxSandboxes = 4
	// The ports sandboxes get, one each, so guests can join from their
	// game.
	DefaultPortMin = 30000
	DefaultPortMax = 30099
	// DefaultTemplates is how many warm templates the node keeps.
	DefaultTemplates = 6
	// MaxSandboxes bounds what a node can be set to.
	MaxSandboxes = 32
)

// UploadLimit is the largest file anyone but the demo org's owners can
// upload in the demo.
const UploadLimit = 10 << 20

// MaxTestMemoryMiB is the largest test memory (raptor.yaml's
// test.memory_mib) an egg can have and still fit. It's above MemoryMiB on
// purpose: the conformance tests give Minecraft servers 2 GB of headroom,
// and one with a single player runs in 1.5 GB (DECISIONS #380).
const MaxTestMemoryMiB = 2048

// Fits reports whether a library egg fits a sandbox, from its raptor.yaml
// test data: only eggs CI runs on every change (tier fast: no multi-GB
// SteamCMD download, no account), within the test memory above. why says
// what's too big when it doesn't.
func Fits(tier string, testMemoryMiB int64) (ok bool, why string) {
	switch {
	case tier != "fast":
		return false, "too big for the demo: its download is large, or it needs an account"
	case testMemoryMiB > MaxTestMemoryMiB:
		return false, "too big for the demo: it needs more memory than a sandbox has"
	}
	return true, ""
}
