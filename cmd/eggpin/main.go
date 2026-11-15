// Command eggpin pins the egg library's images (library/, docs/EGGS.md#egg-library)
// to the digests their tags point to now: every ghcr.io/xena-studios/yolks
// or ghcr.io/pelican-eggs image in an egg's runtimes or install container,
// written as name:tag@sha256:…. An image already pinned is pinned again to
// its tag's current digest. Only those strings in the files change.
//
//	go run ./cmd/eggpin [-check] [dir]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// image is a pinnable image reference in an egg's JSON, quotes included.
var image = regexp.MustCompile(`"(ghcr\.io/(xena-studios/yolks|pelican-eggs/[a-z0-9-]+)):([a-z0-9_.-]+)(@sha256:[0-9a-f]{64})?"`)

func main() {
	check := flag.Bool("check", false, "fail if an image isn't pinned to its tag's current digest, changing nothing")
	flag.Parse()
	dir := "library"
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	if err := run(context.Background(), dir, *check); err != nil {
		fmt.Fprintln(os.Stderr, "eggpin:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dir string, check bool) error {
	r := &resolver{client: &http.Client{Timeout: 30 * time.Second}, digests: map[string]string{}, tokens: map[string]string{}}
	var stale []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), "egg-") || filepath.Ext(path) != ".json" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var failed error
		out := image.ReplaceAllFunc(data, func(m []byte) []byte {
			p := image.FindSubmatch(m)
			repo, tag := string(p[1]), string(p[3])
			digest, err := r.digest(ctx, repo, tag)
			if err != nil {
				failed = errors.Join(failed, fmt.Errorf("%s: %s:%s: %w", path, repo, tag, err))
				return m
			}
			return fmt.Appendf(nil, "%q", repo+":"+tag+"@"+digest)
		})
		if failed != nil {
			return failed
		}
		if string(out) == string(data) {
			return nil
		}
		if check {
			stale = append(stale, path)
			return nil
		}
		fmt.Println("pinned", path)
		return os.WriteFile(path, out, 0o644)
	})
	if err == nil && len(stale) > 0 {
		err = fmt.Errorf("not pinned to the current digests (run task library:pin): %s", strings.Join(stale, ", "))
	}
	return err
}

// resolver looks up tags' digests on GHCR, anonymously, once each.
type resolver struct {
	client  *http.Client
	digests map[string]string
	tokens  map[string]string
}

func (r *resolver) digest(ctx context.Context, repo, tag string) (string, error) {
	key := repo + ":" + tag
	if d, ok := r.digests[key]; ok {
		return d, nil
	}
	name := strings.TrimPrefix(repo, "ghcr.io/")
	token, err := r.token(ctx, name)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://ghcr.io/v2/"+name+"/manifests/"+url.PathEscape(tag), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	// The index (every platform's image), not one platform's manifest.
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, "+
		"application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	res, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	d := res.Header.Get("Docker-Content-Digest")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(d, "sha256:") {
		return "", fmt.Errorf("the registry answered %s", res.Status)
	}
	r.digests[key] = d
	return d, nil
}

func (r *resolver) token(ctx context.Context, name string) (string, error) {
	if t, ok := r.tokens[name]; ok {
		return t, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ghcr.io/token?scope=repository:"+name+":pull", nil)
	if err != nil {
		return "", err
	}
	res, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.Token == "" {
		return "", fmt.Errorf("no pull token for %s (%s)", name, res.Status)
	}
	r.tokens[name] = body.Token
	return body.Token, nil
}
