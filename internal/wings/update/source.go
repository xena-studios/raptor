package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// Release is a published release.
type Release struct {
	Tag        string // "v1.4.2"
	Prerelease bool
}

// Version is the release's version as the binary reports it ("1.4.2").
func (r Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Source lists releases and downloads their files.
type Source interface {
	Releases(ctx context.Context) ([]Release, error)
	// Download writes a release file to w, failing if it's larger than limit.
	Download(ctx context.Context, tag, name string, limit int64, w io.Writer) error
}

// GitHub is the release source: the project's GitHub releases. Drafts
// (releases not yet signed and published) aren't listed.
type GitHub struct {
	Repo        string // "xena-studios/raptor"
	APIURL      string // "https://api.github.com"
	DownloadURL string // "https://github.com"
	Client      *http.Client
}

// Where releases come from. Only the e2e build (e2e.go) changes them.
var (
	releaseAPI      = "https://api.github.com"
	releaseDownload = "https://github.com"
)

// DefaultSource returns the project's GitHub releases.
func DefaultSource() *GitHub {
	return &GitHub{
		Repo:        "xena-studios/raptor",
		APIURL:      releaseAPI,
		DownloadURL: releaseDownload,
		Client:      &http.Client{Timeout: 5 * time.Minute},
	}
}

// Releases lists the 100 most recent releases, enough for any channel.
func (g *GitHub) Releases(ctx context.Context) ([]Release, error) {
	var body strings.Builder
	if err := g.get(ctx, g.APIURL+"/repos/"+g.Repo+"/releases?per_page=100", 4<<20, &body); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	var list []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal([]byte(body.String()), &list); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	out := make([]Release, 0, len(list))
	for _, r := range list {
		if !r.Draft {
			out = append(out, Release{Tag: r.Tag, Prerelease: r.Prerelease})
		}
	}
	return out, nil
}

// Download fetches a release asset.
func (g *GitHub) Download(ctx context.Context, tag, name string, limit int64, w io.Writer) error {
	u := g.DownloadURL + "/" + g.Repo + "/releases/download/" + url.PathEscape(tag) + "/" + url.PathEscape(name)
	if err := g.get(ctx, u, limit, w); err != nil {
		return fmt.Errorf("download %s %s: %w", tag, name, err)
	}
	return nil
}

func (g *GitHub) get(ctx context.Context, u string, limit int64, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := g.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", u, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("%s: larger than %d bytes", u, limit)
	}
	return nil
}

// Latest picks the newest release in a channel: "stable" only has full
// releases, "beta" also has pre-releases. Tags that aren't semantic versions
// are ignored.
func Latest(releases []Release, channel string) (Release, error) {
	var best Release
	for _, r := range releases {
		if !semver.IsValid(r.Tag) || semver.Build(r.Tag) != "" {
			continue
		}
		if channel == "stable" && (r.Prerelease || semver.Prerelease(r.Tag) != "") {
			continue
		}
		if best.Tag == "" || semver.Compare(r.Tag, best.Tag) > 0 {
			best = r
		}
	}
	if best.Tag == "" {
		return Release{}, fmt.Errorf("no %s release published yet", channel)
	}
	return best, nil
}

// errBadVersion is returned for versions that aren't semantic versions.
var errBadVersion = errors.New("not a version like 1.4.2")

// normalize turns "1.4.2" or "v1.4.2" into the tag "v1.4.2".
func normalize(v string) (string, error) {
	tag := "v" + strings.TrimPrefix(strings.TrimSpace(v), "v")
	if !semver.IsValid(tag) || semver.Build(tag) != "" || semver.Canonical(tag) != tag {
		return "", fmt.Errorf("%q: %w", v, errBadVersion)
	}
	return tag, nil
}
