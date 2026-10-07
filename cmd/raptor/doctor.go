package main

import (
	"context"
	"errors"
	"flag"
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
	"github.com/xena-studios/raptor/internal/wings/doctor"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/localapi"
	"github.com/xena-studios/raptor/internal/wings/storage"
)

// errDoctorFailed makes doctor exit 1 when a check failed, after printing.
var errDoctorFailed = errors.New("some checks failed")

func doctorCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	path := fs.String("config", config.DefaultPath, "config file")
	asJSON := fs.Bool("json", false, "print the results as JSON")
	bundle := fs.Bool("bundle", false, "also write a redacted diagnostics bundle (.tar.gz) for support")
	upload := fs.Bool("upload", false, "write the bundle and upload it to Raptor support; prints a support code")
	dir := fs.String("dir", "/var/tmp", "where -bundle writes the bundle")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *upload {
		*bundle = true
	}

	cfg, cfgErr := config.Load(*path)
	if errors.Is(cfgErr, os.ErrNotExist) {
		cfgErr = fmt.Errorf("%s not found: this node hasn't been set up yet", *path)
	}
	if cfgErr != nil {
		cfg = config.Default()
	}
	e := &doctor.Env{
		Config: cfg, ConfigPath: *path, ConfigErr: cfgErr,
		System:      doctor.OS{SpaceFunc: storage.Space},
		VolumeCheck: (&storage.Volume{Path: cfg.Paths.Volumes, Soft: !cfg.Storage.Quotas}).Check,
		Firewall:    firewall.Present,
		HTTPGet:     httpReachable,
		LookupHost:  net.DefaultResolver.LookupHost,
		PublicAddr: func(ctx context.Context) (string, error) {
			return panelSeesMe(ctx, cfg.Panel.URL)
		},
	}
	if dc, err := docker.New(docker.Config{}); err != nil {
		e.DockerErr = err
	} else {
		defer func() { _ = dc.Close() }()
		if _, err := dc.Version(ctx); err != nil {
			e.DockerErr = err
		} else {
			e.Docker = dc
		}
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	e.Status, e.StatusErr = localapi.Dial(cfg.Paths.Socket).GetStatus(sctx, &localv1.GetStatusRequest{})
	cancel()

	if os.Geteuid() != 0 && !*asJSON {
		fmt.Fprintln(os.Stderr, "Not running as root: some checks are skipped or may fail for lack of permission.")
		fmt.Fprintln(os.Stderr)
	}
	results := doctor.Run(ctx, e)
	if *asJSON {
		if err := doctor.PrintJSON(os.Stdout, results); err != nil {
			return err
		}
	} else {
		doctor.Print(os.Stdout, results)
	}
	if *bundle {
		p, err := doctor.Bundle(ctx, e, results, *path, *dir, time.Now())
		if err != nil {
			return fmt.Errorf("bundle: %w", err)
		}
		fmt.Fprintf(os.Stderr, "\nBundle written to %s (redacted: logs and system state, no server files or secrets).\n", p)
		if *upload {
			// Signed with the node key when the node is linked; an
			// unreadable key just means an anonymous upload.
			key, kerr := nodelink.LoadKey(cfg.Identity.Key)
			if kerr != nil {
				key = nil
			}
			uctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			code, err := doctor.Upload(uctx, &http.Client{}, cfg.Panel.URL, p, cfg.NodeID, key, time.Now())
			cancel()
			if err != nil {
				return fmt.Errorf("upload: %w (the bundle is still at %s: send it to support another way)", err, p)
			}
			fmt.Fprintf(os.Stderr, "Uploaded. Your support code is %s: give it to Raptor support.\n", code)
		}
	}
	if doctor.Failed(results) {
		return errDoctorFailed
	}
	return nil
}

// httpReachable reports whether a URL answers at all: any HTTP response
// means DNS, the network, and TLS work.
func httpReachable(ctx context.Context, url string) error {
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

// panelSeesMe asks the Panel which address this node reaches it from.
func panelSeesMe(ctx context.Context, panelURL string) (string, error) {
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
