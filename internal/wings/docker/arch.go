package docker

import (
	"context"
	"fmt"
	goruntime "runtime"
	"slices"

	"github.com/moby/moby/client"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// CheckArch asks the registry which platforms the image has (the manifest
// list), without pulling it.
func (c *Client) CheckArch(ctx context.Context, image string) error {
	arch := goruntime.GOARCH
	var platforms []string
	if res, err := c.api.DistributionInspect(ctx, image, client.DistributionInspectOptions{}); err == nil {
		for _, p := range res.Platforms {
			if p.OS == "" || p.OS == "linux" {
				platforms = append(platforms, p.Architecture)
			}
		}
	} else if local, err := c.api.ImageInspect(ctx, image); err == nil {
		platforms = []string{local.Architecture}
	} else {
		return nil // registry and local copy unavailable: the pull will tell
	}
	if len(platforms) == 0 || slices.Contains(platforms, arch) {
		return nil
	}
	return fmt.Errorf("%w: %s is built for %v, this machine is %s", containers.ErrUnsupportedArch, image, platforms, arch)
}
