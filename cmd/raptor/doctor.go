package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/doctor"
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

	e, closeEnv := doctor.LocalEnv(ctx, *path)
	defer closeEnv()
	cfg := e.Config

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
