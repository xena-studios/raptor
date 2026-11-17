package orgs

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/thunderstore"
)

// sourceThunderstore is Thunderstore (#410): Valheim's and V Rising's
// BepInEx mods, for the thunderstore kind, whose loader is the community.
const sourceThunderstore = "thunderstore"

// hashCallsPerMinute bounds HashAddonFile for an org.
const hashCallsPerMinute = 60

// community is the Thunderstore community the loaders name.
func community(loaders []string) (string, error) {
	for _, l := range loaders {
		if thunderstore.ValidCommunity(l) {
			return l, nil
		}
	}
	return "", invalid(errors.New("thunderstore has mods for valheim and v-rising"))
}

func thunderstoreError(err error) error {
	switch {
	case errors.Is(err, thunderstore.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, thunderstore.ErrNotFound)
	case errors.Is(err, thunderstore.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, thunderstore.ErrUnavailable)
	case errors.Is(err, thunderstore.ErrTooLarge):
		return connect.NewError(connect.CodeFailedPrecondition, thunderstore.ErrTooLarge)
	}
	return err
}

// thunderstoreTitle is a package's name as people read it: Thunderstore's
// names use underscores for spaces.
func thunderstoreTitle(name string) string { return strings.ReplaceAll(name, "_", " ") }

func thunderstoreListed(community string, l thunderstore.Listed) *panelv1.AddonProject {
	r := l.Ref()
	out := &panelv1.AddonProject{
		Id: r.Package(), Slug: r.Package(), Title: thunderstoreTitle(l.Name), Description: l.Description,
		IconUrl: thunderstore.Icon(l.IconURL), Downloads: l.Downloads, Author: l.Namespace,
		Url: thunderstore.PageURL(community, r), Source: sourceThunderstore,
	}
	if thunderstore.ClientOnly(l.Categories) {
		out.ServerSide = "unsupported"
	}
	return out
}

func thunderstorePackage(community string, p thunderstore.Package) *panelv1.AddonProject {
	r := p.Ref()
	out := &panelv1.AddonProject{
		Id: r.Package(), Slug: r.Package(), Title: thunderstoreTitle(p.Name), Description: p.Latest.Description,
		IconUrl: thunderstore.Icon(p.Latest.Icon), Downloads: max(p.Downloads, p.Latest.Downloads),
		Author: cmp.Or(p.Owner, p.Namespace), Url: thunderstore.PageURL(community, r), Source: sourceThunderstore,
	}
	if p.ClientOnly(community) {
		out.ServerSide = "unsupported"
	}
	return out
}

// thunderstoreVersion is a version as an add-on version. Its file has
// Thunderstore's download link and no hash: HashAddonFile gives both.
func (s *Addons) thunderstoreVersion(community string, v thunderstore.Version) *panelv1.AddonVersion {
	r := v.Ref()
	out := &panelv1.AddonVersion{
		Id: r.String(), ProjectId: r.Package(), Name: v.Number, VersionNumber: v.Number, Loaders: []string{community},
		File: &panelv1.AddonFile{Url: s.Thunderstore.DownloadURL(r), Filename: thunderstore.FileName(r)},
	}
	if !v.Created.IsZero() {
		out.PublishedAt = timestamppb.New(v.Created)
	}
	for _, d := range v.Dependencies {
		if dr, ok := thunderstore.ParseVersion(d); ok && !thunderstore.Provided(dr) {
			out.Dependencies = append(out.Dependencies, &panelv1.AddonDependency{ProjectId: dr.Package(), VersionId: dr.String(), Type: "required"})
		}
	}
	return out
}

func (s *Addons) searchThunderstore(ctx context.Context, req *panelv1.SearchAddonsRequest, query string, offset int) (*panelv1.SearchAddonsResponse, error) {
	if req.GetKind() != sourceThunderstore {
		return nil, invalid(errors.New("thunderstore has the thunderstore kind's mods, and only it has them"))
	}
	c, err := community(req.GetLoaders())
	if err != nil {
		return nil, err
	}
	list, total, err := s.Thunderstore.Search(ctx, c, query, offset)
	if err != nil {
		return nil, thunderstoreError(err)
	}
	out := &panelv1.SearchAddonsResponse{Total: int32(min(total, 1<<30))} //nolint:gosec // capped
	for _, l := range list {
		// The BepInEx pack comes with the egg, so it isn't offered.
		if !thunderstore.Provided(l.Ref()) {
			out.Projects = append(out.Projects, thunderstoreListed(c, l))
		}
	}
	return out, nil
}

func (s *Addons) thunderstoreVersions(ctx context.Context, req *panelv1.ListAddonVersionsRequest) (*panelv1.ListAddonVersionsResponse, error) {
	c, err := community(req.GetLoaders())
	if err != nil {
		return nil, err
	}
	r, ok := thunderstore.ParsePackage(req.GetProjectId())
	if !ok {
		return nil, invalid(errors.New("that isn't a Thunderstore package"))
	}
	list, err := s.Thunderstore.Versions(ctx, r)
	if err != nil {
		return nil, thunderstoreError(err)
	}
	out := &panelv1.ListAddonVersionsResponse{}
	for _, v := range list {
		if len(out.Versions) == 50 {
			break
		}
		r.Version = v.Number
		out.Versions = append(out.Versions, s.thunderstoreVersion(c, thunderstore.Version{
			Namespace: r.Namespace, Name: r.Name, Number: v.Number, Created: v.Created, Downloads: v.Downloads,
		}))
	}
	return out, nil
}

// identifyOnThunderstore names the packages Wings unpacked by the version
// each was ("Namespace-Name-1.2.3", from the record Wings keeps beside
// it), and offers the newest version when it's another.
func (s *Addons) identifyOnThunderstore(ctx context.Context, req *panelv1.IdentifyAddonsRequest, out *panelv1.IdentifyAddonsResponse) error {
	if s.Thunderstore == nil {
		return nil
	}
	c, err := community(req.GetLoaders())
	if err != nil {
		return err
	}
	looked := 0
	for _, h := range req.GetHashes() {
		r, ok := thunderstore.ParseVersion(req.GetNames()[h])
		if !ok || looked == thunderstore.MaxNames {
			continue
		}
		looked++
		p, err := s.Thunderstore.Get(ctx, r)
		if errors.Is(err, thunderstore.ErrNotFound) {
			continue
		}
		if err != nil {
			return thunderstoreError(err)
		}
		a := &panelv1.IdentifiedAddon{Sha512: h, Project: thunderstorePackage(c, p)}
		v, err := s.Thunderstore.Version(ctx, r)
		switch {
		case err == nil:
			a.Version = s.thunderstoreVersion(c, v)
		case !errors.Is(err, thunderstore.ErrNotFound):
			return thunderstoreError(err)
		}
		if p.Latest.Number != "" && p.Latest.Number != r.Version &&
			(a.Version == nil || p.Latest.Created.After(v.Created)) {
			a.Latest = s.thunderstoreVersion(c, p.Latest)
		}
		out.Addons = append(out.Addons, a)
	}
	return nil
}

// resolveThunderstore is ResolveAddonDependencies for Thunderstore:
// dependencies are named by version, and each is installed at its newest
// (they're the oldest version a mod was built against). The game's
// BepInEx pack is left out: the egg installs it.
func (s *Addons) resolveThunderstore(ctx context.Context, req *panelv1.ResolveAddonDependenciesRequest) (*panelv1.ResolveAddonDependenciesResponse, error) {
	c, err := community(req.GetLoaders())
	if err != nil {
		return nil, err
	}
	top, ok := thunderstore.ParseVersion(req.GetVersionId())
	if !ok {
		return nil, invalid(errors.New("that isn't a version"))
	}
	if len(req.GetInstalled()) > thunderstore.MaxNames {
		return nil, invalid(errors.New("too many installed packages"))
	}
	for _, p := range req.GetInstalled() {
		if _, ok := thunderstore.ParsePackage(p); !ok {
			return nil, invalid(errors.New("that isn't a Thunderstore package"))
		}
	}
	root, err := s.Thunderstore.Version(ctx, top)
	if err != nil {
		return nil, thunderstoreError(err)
	}
	installed := req.GetInstalled()
	seen := map[string]bool{top.Package(): true}
	out := &panelv1.ResolveAddonDependenciesResponse{}
	var walk func(v thunderstore.Version) error
	walk = func(v thunderstore.Version) error {
		for _, d := range v.Dependencies {
			dr, ok := thunderstore.ParseVersion(d)
			if !ok || thunderstore.Provided(dr) || seen[dr.Package()] || slices.Contains(installed, dr.Package()) {
				continue
			}
			if len(seen) >= maxResolved {
				return nil
			}
			seen[dr.Package()] = true
			p, err := s.Thunderstore.Get(ctx, dr)
			if errors.Is(err, thunderstore.ErrNotFound) || (err == nil && p.Latest.Number == "") {
				out.Unavailable = append(out.Unavailable, &panelv1.AddonProject{
					Id: dr.Package(), Title: thunderstoreTitle(dr.Name), Source: sourceThunderstore,
				})
				continue
			}
			if err != nil {
				return thunderstoreError(err)
			}
			if err := walk(p.Latest); err != nil {
				return err
			}
			out.Required = append(out.Required, &panelv1.ResolvedAddon{
				Project: thunderstorePackage(c, p), Version: s.thunderstoreVersion(c, p.Latest), NeededBy: v.Ref().Package(),
			})
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return out, nil
}

// HashAddonFile implements AddonService (#410).
func (s *Addons) HashAddonFile(ctx context.Context, req *panelv1.HashAddonFileRequest) (*panelv1.HashAddonFileResponse, error) {
	if err := s.allow(ctx, req.GetOrgId()); err != nil {
		return nil, err
	}
	// Each new version is a download for the Panel: fewer than lookups.
	if err := s.Orgs.Auth.RateLimit(ctx, "addons:hash:org:"+req.GetOrgId(), hashCallsPerMinute, time.Minute); err != nil {
		return nil, err
	}
	if req.GetSource() != sourceThunderstore {
		return nil, invalid(errors.New("only thunderstore's files are hashed by the panel"))
	}
	r, ok := thunderstore.ParseVersion(req.GetVersionId())
	if !ok {
		return nil, invalid(errors.New("that isn't a version"))
	}
	f, err := s.Thunderstore.Hash(ctx, r)
	if err != nil {
		return nil, thunderstoreError(err)
	}
	return &panelv1.HashAddonFileResponse{File: &panelv1.AddonFile{Url: f.URL, Filename: f.Name, Size: f.Size, Sha256: f.SHA256}}, nil
}
