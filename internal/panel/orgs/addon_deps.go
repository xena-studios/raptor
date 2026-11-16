package orgs

import (
	"context"
	"errors"
	"slices"
	"strings"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/factorio"
	"github.com/xena-studios/raptor/internal/panel/modrinth"
)

// maxResolved is how many projects a resolution looks at, so a cycle or a
// huge modpack-like tree can't make the Panel call Modrinth without end.
const maxResolved = 25

// ResolveAddonDependencies implements AddonService (#434).
func (s *Addons) ResolveAddonDependencies(ctx context.Context, req *panelv1.ResolveAddonDependenciesRequest) (*panelv1.ResolveAddonDependenciesResponse, error) {
	if err := s.check(ctx, req.GetOrgId(), "", req.GetLoaders(), req.GetGameVersion()); err != nil {
		return nil, err
	}
	if req.GetSource() == sourceFactorio {
		return s.resolveFactorio(ctx, req)
	}
	if req.GetSource() == sourceCurseForge {
		return s.resolveCurseForge(ctx, req)
	}
	if req.GetSource() == sourceThunderstore {
		return s.resolveThunderstore(ctx, req)
	}
	if !projectRef.MatchString(req.GetVersionId()) {
		return nil, invalid(errors.New("that isn't a version"))
	}
	if len(req.GetInstalled()) > modrinth.MaxHashes {
		return nil, invalid(errors.New("too many installed projects"))
	}
	for _, p := range req.GetInstalled() {
		if !projectRef.MatchString(p) {
			return nil, invalid(errors.New("that isn't a project"))
		}
	}
	root, err := s.Modrinth.Version(ctx, req.GetVersionId())
	if err != nil {
		return nil, modrinthError(err)
	}
	r := &resolver{
		s: s, ctx: ctx, loaders: req.GetLoaders(), gameVersion: req.GetGameVersion(),
		installed: req.GetInstalled(), seen: map[string]bool{root.ProjectID: true},
		versions: map[string]modrinth.Version{}, incompatible: map[string]bool{},
	}
	if err := r.walk(root, true); err != nil {
		return nil, err
	}
	return r.result()
}

type pick struct {
	version  modrinth.Version
	neededBy string
}

type resolver struct {
	s           *Addons
	ctx         context.Context //nolint:containedctx // one request's
	loaders     []string
	gameVersion string
	installed   []string

	seen         map[string]bool
	versions     map[string]modrinth.Version
	required     []pick // dependencies first: appended after their own
	optional     []pick
	incompatible map[string]bool
	unavailable  []string // project IDs, or "file:" + a file name
}

// walk follows a version's dependencies: required ones all the way down,
// optional ones only for the version being installed (top).
func (r *resolver) walk(v modrinth.Version, top bool) error {
	for _, d := range v.Dependencies {
		if d.ProjectID == "" && d.VersionID == "" {
			if d.Type == "required" && d.FileName != "" {
				r.unavailable = append(r.unavailable, "file:"+d.FileName)
			}
			continue
		}
		switch d.Type {
		case "incompatible":
			if slices.Contains(r.installed, d.ProjectID) {
				r.incompatible[d.ProjectID] = true
			}
			continue
		case "required":
		case "optional":
			if !top {
				continue
			}
		default: // embedded: it's inside the file already
			continue
		}
		if d.ProjectID != "" && (r.seen[d.ProjectID] || slices.Contains(r.installed, d.ProjectID)) {
			continue
		}
		if len(r.seen) >= maxResolved {
			return nil
		}
		dv, ok, err := r.version(d)
		if err != nil {
			return err
		}
		project := d.ProjectID
		if ok {
			project = dv.ProjectID
		}
		if project == "" || r.seen[project] || slices.Contains(r.installed, project) {
			continue
		}
		r.seen[project] = true
		if !ok {
			if d.Type == "required" {
				r.unavailable = append(r.unavailable, project)
			}
			continue
		}
		if d.Type == "optional" {
			r.optional = append(r.optional, pick{dv, v.ProjectID})
			continue
		}
		if err := r.walk(dv, false); err != nil {
			return err
		}
		r.required = append(r.required, pick{dv, v.ProjectID})
	}
	return nil
}

// version is the version a dependency names, or the project's newest for
// the loaders and game version; ok is false when there's none with a file.
func (r *resolver) version(d modrinth.Dependency) (modrinth.Version, bool, error) {
	if d.VersionID != "" {
		v, err := r.s.Modrinth.Version(r.ctx, d.VersionID)
		if errors.Is(err, modrinth.ErrNotFound) {
			return v, false, nil
		}
		if err != nil {
			return v, false, modrinthError(err)
		}
		_, ok := v.Main()
		return v, ok, nil
	}
	list, err := r.s.Modrinth.Versions(r.ctx, d.ProjectID, r.loaders, r.gameVersion)
	if errors.Is(err, modrinth.ErrNotFound) {
		return modrinth.Version{}, false, nil
	}
	if err != nil {
		return modrinth.Version{}, false, modrinthError(err)
	}
	for _, v := range list {
		if _, ok := v.Main(); ok {
			return v, true, nil
		}
	}
	return modrinth.Version{}, false, nil
}

func (r *resolver) result() (*panelv1.ResolveAddonDependenciesResponse, error) {
	var ids []string
	add := func(id string) {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, p := range append(slices.Clone(r.required), r.optional...) {
		add(p.version.ProjectID)
	}
	for id := range r.incompatible {
		add(id)
	}
	for _, id := range r.unavailable {
		if len(id) < 5 || id[:5] != "file:" {
			add(id)
		}
	}
	projects := map[string]*panelv1.AddonProject{}
	if len(ids) > 0 {
		list, err := r.s.Modrinth.Projects(r.ctx, ids)
		if err != nil {
			return nil, modrinthError(err)
		}
		for _, p := range list {
			projects[p.Key()] = projectProto(p)
		}
	}
	named := func(id string) *panelv1.AddonProject {
		if p, ok := projects[id]; ok {
			return p
		}
		return &panelv1.AddonProject{Id: id, Title: id, Source: sourceModrinth}
	}
	out := &panelv1.ResolveAddonDependenciesResponse{}
	for _, p := range r.required {
		out.Required = append(out.Required, &panelv1.ResolvedAddon{Project: named(p.version.ProjectID), Version: versionProto(p.version), NeededBy: p.neededBy})
	}
	for _, p := range r.optional {
		out.Optional = append(out.Optional, &panelv1.ResolvedAddon{Project: named(p.version.ProjectID), Version: versionProto(p.version), NeededBy: p.neededBy})
	}
	for _, id := range ids {
		if r.incompatible[id] {
			out.Incompatible = append(out.Incompatible, named(id))
		}
	}
	for _, id := range r.unavailable {
		if len(id) > 5 && id[:5] == "file:" {
			out.Unavailable = append(out.Unavailable, &panelv1.AddonProject{Title: id[5:]})
			continue
		}
		out.Unavailable = append(out.Unavailable, named(id))
	}
	return out, nil
}

// resolveFactorio is ResolveAddonDependencies for Factorio's mod portal:
// the version is "mod@version", and dependencies are named by mod, each
// at its newest release for the server's Factorio version.
func (s *Addons) resolveFactorio(ctx context.Context, req *panelv1.ResolveAddonDependenciesRequest) (*panelv1.ResolveAddonDependenciesResponse, error) {
	mod, version, ok := strings.Cut(req.GetVersionId(), "@")
	if !ok || !factorio.ValidName(mod) || version == "" {
		return nil, invalid(errors.New("that isn't a version"))
	}
	for _, p := range req.GetInstalled() {
		if !factorio.ValidName(p) {
			return nil, invalid(errors.New("that isn't a mod"))
		}
	}
	major := factorio.Major(req.GetGameVersion())
	root, err := s.Factorio.Get(ctx, mod)
	if err != nil {
		return nil, modrinthError(err)
	}
	var top factorio.Release
	for _, r := range root.Releases {
		if r.Version == version {
			top = r
		}
	}
	if top.Version == "" {
		return nil, connect.NewError(connect.CodeNotFound, factorio.ErrNotFound)
	}
	installed := req.GetInstalled()
	seen := map[string]bool{mod: true}
	out := &panelv1.ResolveAddonDependenciesResponse{}
	var walk func(r factorio.Release, by string, first bool) error
	walk = func(r factorio.Release, by string, first bool) error {
		for _, d := range factorio.Dependencies(r) {
			switch {
			case d.Type == "incompatible":
				if slices.Contains(installed, d.Name) {
					out.Incompatible = append(out.Incompatible, &panelv1.AddonProject{Id: d.Name, Title: d.Name, Source: sourceFactorio})
				}
				continue
			case d.Type == "optional" && !first, seen[d.Name], slices.Contains(installed, d.Name):
				continue
			}
			if len(seen) >= maxResolved {
				return nil
			}
			seen[d.Name] = true
			m, err := s.Factorio.Get(ctx, d.Name)
			if errors.Is(err, factorio.ErrNotFound) {
				if d.Type == "required" {
					out.Unavailable = append(out.Unavailable, &panelv1.AddonProject{Id: d.Name, Title: d.Name, Source: sourceFactorio})
				}
				continue
			}
			if err != nil {
				return modrinthError(err)
			}
			rels := m.ReleasesFor(major)
			if len(rels) == 0 {
				if d.Type == "required" {
					out.Unavailable = append(out.Unavailable, factorioProject(m.Mod))
				}
				continue
			}
			picked := &panelv1.ResolvedAddon{Project: factorioProject(m.Mod), Version: factorioVersion(m.Name, rels[0]), NeededBy: by}
			if d.Type == "optional" {
				out.Optional = append(out.Optional, picked)
				continue
			}
			if err := walk(rels[0], m.Name, false); err != nil {
				return err
			}
			out.Required = append(out.Required, picked)
		}
		return nil
	}
	if err := walk(top, mod, true); err != nil {
		return nil, err
	}
	return out, nil
}
