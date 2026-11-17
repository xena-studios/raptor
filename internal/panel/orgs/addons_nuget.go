package orgs

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/nuget"
	"github.com/xena-studios/raptor/internal/shared/nupkg"
)

// OpenMod's plugins (#415): NuGet packages tagged openmod-plugin, the
// openmod kind's only source.
const (
	sourceNuGet = "nuget"
	kindOpenMod = "openmod"
)

// nugetVersions is how many of a package's versions are offered: each
// needs its catalog entry for the hash.
const nugetVersions = 10

func nugetError(err error) error {
	switch {
	case errors.Is(err, nuget.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, nuget.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, nuget.ErrUnavailable)
	}
	return err
}

// isNuGet reports whether a request is for OpenMod's plugins, and refuses
// one that mixes the kind and another source.
func isNuGet(kind, source string) (bool, error) {
	if kind != kindOpenMod && source != sourceNuGet {
		return false, nil
	}
	if (kind != "" && kind != kindOpenMod) || source != sourceNuGet {
		return true, invalid(errors.New("openmod's plugins come from nuget, and only nuget has them"))
	}
	return true, nil
}

func nugetProject(id, title, description, icon, authors string, downloads int64) *panelv1.AddonProject {
	return &panelv1.AddonProject{
		Id: strings.ToLower(id), Slug: id, Title: cmp.Or(title, id), Description: description, IconUrl: icon,
		Downloads: downloads, Author: authors, Url: "https://www.nuget.org/packages/" + id, Source: sourceNuGet,
	}
}

func nugetVersionProject(v nuget.Version) *panelv1.AddonProject {
	icon := nuget.Icon(nuget.Package{IconURL: v.IconURL})
	return nugetProject(v.ID, v.Title, v.Description, icon, v.Authors, 0)
}

// nugetVersion is a version as an add-on version: its ID is "id@version",
// its file the package (checked by NuGet's SHA-512), and the file Wings
// writes is ID@version.dll. f is nil when it's only being named.
func nugetVersion(v nuget.Version, f *nuget.File) *panelv1.AddonVersion {
	out := &panelv1.AddonVersion{
		Id: strings.ToLower(v.ID) + "@" + v.Version, ProjectId: strings.ToLower(v.ID), Name: v.Version, VersionNumber: v.Version,
		Loaders: []string{kindOpenMod},
	}
	if v.Prerelease() {
		out.VersionNumber += " (pre-release)"
	}
	if !v.Published.IsZero() && v.Published.Year() > 1900 { // unlisted ones say 1900
		out.PublishedAt = timestamppb.New(v.Published)
	}
	if f != nil {
		out.File = &panelv1.AddonFile{Url: f.URL, Filename: nupkg.FileName(v.ID, v.Version), Size: f.Size, Sha512: f.Sha512}
	}
	for _, d := range v.Dependencies() {
		out.Dependencies = append(out.Dependencies, &panelv1.AddonDependency{ProjectId: strings.ToLower(d.ID), Type: "required"})
	}
	return out
}

func (s *Addons) searchNuGet(ctx context.Context, query string, offset int) (*panelv1.SearchAddonsResponse, error) {
	pkgs, total, err := s.NuGet.SearchPlugins(ctx, query, offset)
	if err != nil {
		return nil, nugetError(err)
	}
	out := &panelv1.SearchAddonsResponse{Total: int32(min(total, 1<<30))} //nolint:gosec // capped
	for _, p := range pkgs {
		out.Projects = append(out.Projects, nugetProject(p.ID, p.Title, p.Description, nuget.Icon(p), strings.Join(p.Authors, ", "), p.Downloads))
	}
	return out, nil
}

// newestNuGet is a package's newest version Wings can install, a release
// unless it has only pre-releases, or ok false.
func (s *Addons) newestNuGet(ctx context.Context, versions []nuget.Version) (nuget.Version, nuget.File, bool, error) {
	for _, pre := range []bool{false, true} {
		tried := 0
		for _, v := range versions {
			if v.Prerelease() != pre || tried == 3 {
				continue
			}
			tried++
			f, ok, err := s.NuGet.File(ctx, v)
			if err != nil {
				return v, f, false, nugetError(err)
			}
			if ok {
				return v, f, true, nil
			}
		}
	}
	return nuget.Version{}, nuget.File{}, false, nil
}

func (s *Addons) nugetVersions(ctx context.Context, pkg string) (*panelv1.ListAddonVersionsResponse, error) {
	if !nupkg.ValidID(pkg) {
		return nil, invalid(errors.New("that isn't a package"))
	}
	versions, err := s.NuGet.Versions(ctx, pkg)
	if err != nil {
		return nil, nugetError(err)
	}
	// Releases first, newest first; pre-releases after them.
	slices.SortStableFunc(versions, func(a, b nuget.Version) int {
		switch {
		case a.Prerelease() == b.Prerelease():
			return 0
		case a.Prerelease():
			return 1
		}
		return -1
	})
	out := &panelv1.ListAddonVersionsResponse{}
	for _, v := range versions[:min(len(versions), nugetVersions)] {
		f, ok, err := s.NuGet.File(ctx, v)
		if err != nil {
			return nil, nugetError(err)
		}
		if ok {
			out.Versions = append(out.Versions, nugetVersion(v, &f))
		}
	}
	return out, nil
}

// identifyOnNuGet names OpenMod plugins by their files' names, which
// Raptor gives them (Kr4ken.NewEssentials@1.5.1.dll): the version named,
// and the package's newest release when it's a different one. At most
// nuget.MaxNames are looked up.
func (s *Addons) identifyOnNuGet(ctx context.Context, req *panelv1.IdentifyAddonsRequest, out *panelv1.IdentifyAddonsResponse) error {
	if s.NuGet == nil || !slices.Contains(req.GetLoaders(), kindOpenMod) {
		return nil
	}
	looked := 0
	for _, h := range req.GetHashes() {
		pkg, ver, ok := nupkg.ParseFileName(req.GetNames()[h])
		if !ok || looked == nuget.MaxNames {
			continue
		}
		looked++
		versions, err := s.NuGet.Versions(ctx, pkg)
		if errors.Is(err, nuget.ErrNotFound) {
			continue
		}
		if err != nil {
			return nugetError(err)
		}
		i := slices.IndexFunc(versions, func(v nuget.Version) bool { return strings.EqualFold(v.Version, ver) })
		if i < 0 {
			continue
		}
		a := &panelv1.IdentifiedAddon{Sha512: h, Project: nugetVersionProject(versions[i]), Version: nugetVersion(versions[i], nil)}
		// Only a newer release is offered, so an update is never a step
		// back or onto a pre-release.
		if newest, f, ok, err := s.newestNuGet(ctx, versions[:i]); err != nil {
			return err
		} else if ok && !newest.Prerelease() {
			a.Latest = nugetVersion(newest, &f)
		}
		out.Addons = append(out.Addons, a)
	}
	return nil
}

// resolveNuGet is ResolveAddonDependencies for OpenMod's plugins: the
// version is "id@version", and each dependency is installed at its newest
// release (NuGet's ranges are minimums in practice). Dependencies OpenMod
// brings are left out; ones Wings can't install (a library with more
// than one assembly, or none for Unturned) are unavailable, for
// `openmod install`.
func (s *Addons) resolveNuGet(ctx context.Context, req *panelv1.ResolveAddonDependenciesRequest) (*panelv1.ResolveAddonDependenciesResponse, error) {
	pkg, ver, ok := strings.Cut(req.GetVersionId(), "@")
	if !ok || !nupkg.ValidID(pkg) || !nupkg.ValidVersion(ver) {
		return nil, invalid(errors.New("that isn't a version"))
	}
	if len(req.GetInstalled()) > nuget.MaxNames*4 {
		return nil, invalid(errors.New("too many installed projects"))
	}
	installed := map[string]bool{}
	for _, p := range req.GetInstalled() {
		if !nupkg.ValidID(p) {
			return nil, invalid(errors.New("that isn't a package"))
		}
		installed[strings.ToLower(p)] = true
	}
	versions, err := s.NuGet.Versions(ctx, pkg)
	if err != nil {
		return nil, nugetError(err)
	}
	i := slices.IndexFunc(versions, func(v nuget.Version) bool { return strings.EqualFold(v.Version, ver) })
	if i < 0 {
		return nil, connect.NewError(connect.CodeNotFound, nuget.ErrNotFound)
	}
	seen := map[string]bool{strings.ToLower(pkg): true}
	out := &panelv1.ResolveAddonDependenciesResponse{}
	var walk func(v nuget.Version) error
	walk = func(v nuget.Version) error {
		for _, d := range v.Dependencies() {
			id := strings.ToLower(d.ID)
			if seen[id] || installed[id] {
				continue
			}
			if len(seen) >= maxResolved {
				return nil
			}
			seen[id] = true
			dvs, err := s.NuGet.Versions(ctx, d.ID)
			if errors.Is(err, nuget.ErrNotFound) {
				out.Unavailable = append(out.Unavailable, nugetProject(d.ID, "", "", "", "", 0))
				continue
			}
			if err != nil {
				return nugetError(err)
			}
			dv, f, ok, err := s.newestNuGet(ctx, dvs)
			if err != nil {
				return err
			}
			if !ok {
				out.Unavailable = append(out.Unavailable, nugetProject(d.ID, "", "", "", "", 0))
				continue
			}
			if err := walk(dv); err != nil {
				return err
			}
			out.Required = append(out.Required, &panelv1.ResolvedAddon{Project: nugetVersionProject(dv), Version: nugetVersion(dv, &f), NeededBy: strings.ToLower(v.ID)})
		}
		return nil
	}
	if err := walk(versions[i]); err != nil {
		return nil, err
	}
	return out, nil
}
