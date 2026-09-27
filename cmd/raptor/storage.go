package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/storage"
)

// Smallest volume `raptor storage setup` creates. XFS itself needs ~300 MB.
const minVolume = 1 << 30

// storageCmd is `raptor storage status|setup|grow` (docs/WINGS.md#disk-quotas).
// It works on the box directly, not through Wings, so it also works while
// Wings is stopped.
func storageCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: raptor storage status|setup|grow [flags]")
	}
	fs := flag.NewFlagSet("storage "+args[0], flag.ContinueOnError)
	path := fs.String("config", config.DefaultPath, "config file")
	var size *string
	if args[0] == "setup" || args[0] == "grow" {
		size = fs.String("size", "", `volume size, e.g. "200GiB" (required)`)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if errors.Is(err, os.ErrNotExist) {
		cfg = config.Default()
	} else if err != nil {
		return err
	}
	vol := cfg.Paths.Volumes
	switch args[0] {
	case "status":
		return storageStatus(&cfg)
	case "setup", "grow":
		if os.Geteuid() != 0 {
			return errors.New("must be run as root")
		}
		if *size == "" {
			return errors.New("-size is required")
		}
		n, err := config.ParseByteSize(*size)
		if err != nil {
			return err
		}
		if args[0] == "setup" {
			return storageSetup(ctx, &cfg, int64(n))
		}
		m, ok, err := storage.FindMount(vol)
		if err != nil {
			return err
		}
		image := storage.BackingFile(m)
		if !ok || image == "" {
			return fmt.Errorf("%s isn't a Raptor volume image, so there's nothing to grow (a volume on its own disk is grown with the disk and xfs_growfs)", vol)
		}
		if err := checkHostSpace(image, int64(n)-fileSize(image), int64(cfg.Limits.HostDiskMinFree)); err != nil {
			return err
		}
		if err := storage.Grow(ctx, storage.ImageSpec{Image: image, Mountpoint: vol}, int64(n)); err != nil {
			return err
		}
		fmt.Printf("grew %s to %s; servers kept running\n", vol, human(int64(n)))
		return nil
	default:
		return fmt.Errorf("unknown storage command %q", args[0])
	}
}

func storageStatus(cfg *config.Config) error {
	v := &storage.Volume{Path: cfg.Paths.Volumes, Soft: !cfg.Storage.Quotas}
	fmt.Printf("Volume   %s\n", v.Path)
	if v.Soft {
		fmt.Println("Limits   soft: checked by scanning every 5 minutes (storage.quotas is off)")
	} else {
		fmt.Println("Limits   enforced by XFS project quotas")
	}
	m, ok, err := storage.FindMount(v.Path)
	switch {
	case err != nil:
		return err
	case !ok:
		fmt.Println("Mount    none")
	default:
		src := m.Source
		if img := storage.BackingFile(m); img != "" {
			dio := "off"
			if storage.DirectIO(m) {
				dio = "on"
			}
			src = fmt.Sprintf("image %s on %s, direct I/O %s", img, m.Source, dio)
		}
		fmt.Printf("Mount    %s (%s, project quotas %s)\n", m.FSType, src, yesNo(m.HasProjectQuota()))
	}
	if total, free, err := storage.Space(v.Path); err == nil {
		fmt.Printf("Space    %s free of %s\n", human(free), human(total))
	}
	if err := v.Check(); err != nil {
		fmt.Printf("Status   ✗ %s\n", strings.TrimSuffix(err.Error(), " (run `raptor storage status`)"))
		if !ok && !v.Soft {
			fmt.Println("         Mount an XFS disk there with prjquota, or run `raptor storage setup -size <size>`.")
		}
		return errors.New("servers can't start until the volume is fixed")
	}
	fmt.Println("Status   ✓ ready")
	return nil
}

// storageSetup creates the volume image (quota tier 2) for boxes without an
// XFS disk for server data.
func storageSetup(ctx context.Context, cfg *config.Config, size int64) error {
	vol := cfg.Paths.Volumes
	if m, ok, err := storage.FindMount(vol); err != nil {
		return err
	} else if ok {
		return fmt.Errorf("%s is already mounted (%s from %s); use `raptor storage grow` to enlarge an image", vol, m.FSType, m.Source)
	}
	if size < minVolume {
		return fmt.Errorf("the volume must be at least %s", human(minVolume))
	}
	// Mounting over existing server data would hide it.
	if entries, err := os.ReadDir(vol); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s isn't empty; move its contents out before creating the volume", vol)
	}
	image := vol + ".xfs"
	// On a new box Wings hasn't created its state directory yet.
	if err := os.MkdirAll(filepath.Dir(image), 0o700); err != nil { //nolint:gosec // path from the root-owned config
		return err
	}
	if err := checkHostSpace(image, size, int64(cfg.Limits.HostDiskMinFree)); err != nil {
		return err
	}
	fmt.Printf("creating a %s XFS volume at %s (mounted at %s)…\n", human(size), image, vol)
	if err := storage.CreateImage(ctx, storage.ImageSpec{Image: image, Mountpoint: vol, Size: size, UnitDir: "/etc/systemd/system"}); err != nil {
		return err
	}
	if !cfg.Storage.Quotas {
		fmt.Println("note: storage.quotas is off in the config, so limits stay soft until it's turned on")
	}
	fmt.Println("done; the volume mounts at every boot before Wings starts")
	return nil
}

// checkHostSpace refuses to take space the host needs: after adding grow
// bytes, the filesystem holding path must keep limits.host_disk_min_free.
func checkHostSpace(path string, grow, minFree int64) error {
	dir := path
	if _, err := os.Stat(dir); err != nil { //nolint:gosec // paths from the root-owned config
		dir = filepath.Dir(path)
	}
	_, free, err := storage.Space(dir)
	if err != nil {
		return err
	}
	if free-grow < minFree {
		return fmt.Errorf("not enough host disk: %s free, %s needed, and %s must stay free for the host (limits.host_disk_min_free)",
			human(free), human(grow), human(minFree))
	}
	return nil
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func yesNo(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// human formats bytes in binary units ("12.5 GiB").
func human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
