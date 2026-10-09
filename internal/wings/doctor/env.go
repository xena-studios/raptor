package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/localapi"
	"github.com/xena-studios/raptor/internal/wings/storage"
)

// LocalEnv is the box's own Env, as `raptor doctor` sees it and as Wings
// runs it for the Panel's node health page: the config at path, Docker,
// and Wings' status over its local socket. close releases Docker.
func LocalEnv(ctx context.Context, path string) (e *Env, closeEnv func()) {
	cfg, cfgErr := config.Load(path)
	if errors.Is(cfgErr, os.ErrNotExist) {
		cfgErr = fmt.Errorf("%s not found: this node hasn't been set up yet", path)
	}
	if cfgErr != nil {
		cfg = config.Default()
	}
	e = &Env{
		Config: cfg, ConfigPath: path, ConfigErr: cfgErr,
		System:      OS{SpaceFunc: storage.Space},
		VolumeCheck: (&storage.Volume{Path: cfg.Paths.Volumes, Soft: !cfg.Storage.Quotas}).Check,
		Firewall:    firewall.Present,
		HTTPGet:     HTTPReachable,
		LookupHost:  net.DefaultResolver.LookupHost,
		PublicAddr: func(ctx context.Context) (string, error) {
			return PanelSeesMe(ctx, cfg.Panel.URL)
		},
	}
	closeEnv = func() {}
	if dc, err := docker.New(docker.Config{}); err != nil {
		e.DockerErr = err
	} else {
		closeEnv = func() { _ = dc.Close() }
		if _, err := dc.Version(ctx); err != nil {
			e.DockerErr = err
		} else {
			e.Docker = dc
		}
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	e.Status, e.StatusErr = localapi.Dial(cfg.Paths.Socket).GetStatus(sctx, &localv1.GetStatusRequest{})
	cancel()
	return e, closeEnv
}

// HTTPReachable reports whether a URL answers at all: any HTTP response
// means DNS, the network, and TLS work.
func HTTPReachable(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}
	c := &http.Client{Timeout: 10 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

// PanelSeesMe asks the Panel which address this node reaches it from.
func PanelSeesMe(ctx context.Context, panelURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(panelURL, "/")+nodelink.AddressPath, nil)
	if err != nil {
		return "", err
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(res.Body, 256))
	if err != nil {
		return "", err
	}
	a, err := netip.ParseAddr(strings.TrimSpace(string(b)))
	if res.StatusCode != http.StatusOK || err != nil {
		return "", fmt.Errorf("the Panel answered %s", res.Status)
	}
	return a.String(), nil
}
