package orgs

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/curseforge"
)

// sourceCurseForge is CurseForge (#412): Minecraft mods and Bukkit
// plugins, only once the Panel has an API key.
const sourceCurseForge = "curseforge"

var errNoCurseForge = connect.NewError(connect.CodeFailedPrecondition, errors.New("curseforge isn't set up on this Panel"))

// ListAddonSources implements AddonService.
func (s *Addons) ListAddonSources(ctx context.Context, req *panelv1.ListAddonSourcesRequest) (*panelv1.ListAddonSourcesResponse, error) {
	if err := s.allow(ctx, req.GetOrgId()); err != nil {
		return nil, err
	}
	out := &panelv1.ListAddonSourcesResponse{}
	if s.CurseForge.On() {
		out.Sources = append(out.Sources, sourceCurseForge)
	}
	return out, nil
}

// curseForgeClass is the CurseForge class and loader for an add-on kind:
// mods for the mod loaders, Bukkit plugins for Paper's family.
func curseForgeClass(kind string, loaders []string) (int, string, error) {
	switch kind {
	case "mods", "":
		for _, l := range loaders {
			if curseforge.LoaderType(l) != 0 {
				return curseforge.ClassMods, l, nil
			}
		}
		if kind == "mods" {
			return 0, "", invalid(errors.New("curseforge has no mods for these loaders"))
		}
	}
	if kind == "plugins" || kind == "" {
		for _, l := range loaders {
			if slices.Contains([]string{"paper", "spigot", "bukkit", "purpur", "folia"}, l) {
				return curseforge.ClassPlugins, l, nil
			}
		}
	}
	return 0, "", invalid(errors.New("curseforge has mods and Bukkit plugins only"))
}

func curseForgeProject(m curseforge.Mod) *panelv1.AddonProject {
	id := strconv.FormatInt(m.ID, 10)
	return &panelv1.AddonProject{
		Id: id, Slug: m.Slug, Title: m.Name, Description: m.Summary, IconUrl: m.Icon(),
		Downloads: m.DownloadCount, Author: m.Author(), Url: m.Page(), Source: sourceCurseForge,
	}
}

// curseForgeVersion is a file as an add-on version. A file Wings can't
// download (its author turned off other apps' downloads) has only a
// manual_url, to download it by hand; nil if it has neither.
func curseForgeVersion(m curseforge.Mod, f curseforge.File) *panelv1.AddonVersion {
	file := &panelv1.AddonFile{Filename: f.FileName, Size: f.FileLength}
	switch {
	case f.Installable() && m.Distributable():
		file.Url, file.Sha1 = f.DownloadURL, f.SHA1()
	case m.Slug != "":
		file.ManualUrl = m.Page() + "/files/" + strconv.FormatInt(f.ID, 10)
	default:
		return nil
	}
	out := &panelv1.AddonVersion{
		Id: strconv.FormatInt(f.ID, 10), ProjectId: strconv.FormatInt(f.ModID, 10),
		Name: f.DisplayName, VersionNumber: f.DisplayName,
		GameVersions: f.Versions(), Loaders: f.Loaders(), File: file,
	}
	if m.ClassID == curseforge.ClassPlugins {
		out.Loaders = []string{"bukkit"}
	}
	if !f.FileDate.IsZero() {
		out.PublishedAt = timestamppb.New(f.FileDate)
	}
	for _, d := range f.Dependencies {
		if t := curseforge.Relation(d.RelationType); t != "" {
			out.Dependencies = append(out.Dependencies, &panelv1.AddonDependency{ProjectId: strconv.FormatInt(d.ModID, 10), Type: t})
		}
	}
	return out
}

func (s *Addons) searchCurseForge(ctx context.Context, req *panelv1.SearchAddonsRequest, query string, offset int) (*panelv1.SearchAddonsResponse, error) {
	if !s.CurseForge.On() {
		return nil, errNoCurseForge
	}
	class, loader, err := curseForgeClass(req.GetKind(), req.GetLoaders())
	if err != nil {
		return nil, err
	}
	mods, total, err := s.CurseForge.Search(ctx, class, loader, req.GetGameVersion(), query, offset)
	if err != nil {
		return nil, curseForgeError(err)
	}
	out := &panelv1.SearchAddonsResponse{Total: int32(total)} //nolint:gosec // at most 10,000
	for _, m := range mods {
		out.Projects = append(out.Projects, curseForgeProject(m))
	}
	return out, nil
}

func (s *Addons) curseForgeVersions(ctx context.Context, req *panelv1.ListAddonVersionsRequest) (*panelv1.ListAddonVersionsResponse, error) {
	if !s.CurseForge.On() {
		return nil, errNoCurseForge
	}
	id, err := strconv.ParseInt(req.GetProjectId(), 10, 64)
	if err != nil || id <= 0 {
		return nil, invalid(errors.New("that isn't a CurseForge project"))
	}
	m, err := s.CurseForge.Mod(ctx, id)
	if err != nil {
		return nil, curseForgeError(err)
	}
	if m.ClassID != curseforge.ClassMods && m.ClassID != curseforge.ClassPlugins {
		return nil, invalid(errors.New("that isn't a mod or a plugin"))
	}
	_, loader, err := curseForgeClass("", req.GetLoaders())
	if err != nil {
		return nil, err
	}
	if m.ClassID == curseforge.ClassPlugins {
		loader = ""
	}
	files, err := s.CurseForge.Files(ctx, id, loader, req.GetGameVersion())
	if err != nil {
		return nil, curseForgeError(err)
	}
	out := &panelv1.ListAddonVersionsResponse{}
	for _, f := range files {
		if v := curseForgeVersion(m, f); v != nil {
			out.Versions = append(out.Versions, v)
		}
	}
	return out, nil
}

// identifyOnCurseForge looks up by fingerprint the files neither Modrinth
// nor Hangar knows, and offers each the newest file for the loaders and
// game version.
func (s *Addons) identifyOnCurseForge(ctx context.Context, req *panelv1.IdentifyAddonsRequest, out *panelv1.IdentifyAddonsResponse) error {
	if !s.CurseForge.On() || len(req.GetCurseforge()) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, a := range out.GetAddons() {
		known[a.GetSha512()] = true
	}
	bySum := map[uint32]string{}
	var prints []uint32
	for _, h := range req.GetHashes() {
		fp, ok := req.GetCurseforge()[h]
		if !ok || known[h] || len(prints) == curseforge.MaxFingerprints {
			continue
		}
		if _, dup := bySum[fp]; !dup {
			prints = append(prints, fp)
		}
		bySum[fp] = h
	}
	if len(prints) == 0 {
		return nil
	}
	files, err := s.CurseForge.ByFingerprint(ctx, prints)
	if err != nil {
		if errors.Is(err, curseforge.ErrNotFound) {
			return nil
		}
		return curseForgeError(err)
	}
	var ids []int64
	for _, f := range files {
		if !slices.Contains(ids, f.ModID) {
			ids = append(ids, f.ModID)
		}
	}
	mods := map[int64]curseforge.Mod{}
	if len(ids) > 0 {
		list, err := s.CurseForge.Mods(ctx, ids)
		if err != nil {
			return curseForgeError(err)
		}
		for _, m := range list {
			mods[m.ID] = m
		}
	}
	_, loader, _ := curseForgeClass("", req.GetLoaders())
	for _, fp := range prints {
		f, ok := files[fp]
		m, found := mods[f.ModID]
		if !ok || !found {
			continue
		}
		a := &panelv1.IdentifiedAddon{Sha512: bySum[fp], Project: curseForgeProject(m), Version: curseForgeVersion(m, f)}
		l := loader
		if m.ClassID == curseforge.ClassPlugins {
			l = ""
		}
		newer, err := s.CurseForge.Files(ctx, m.ID, l, req.GetGameVersion())
		if err != nil && !errors.Is(err, curseforge.ErrNotFound) {
			return curseForgeError(err)
		}
		if len(newer) > 0 && newer[0].FileDate.After(f.FileDate) {
			a.Latest = curseForgeVersion(m, newer[0])
		}
		out.Addons = append(out.Addons, a)
	}
	return nil
}

func curseForgeError(err error) error {
	switch {
	case errors.Is(err, curseforge.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, curseforge.ErrNotFound)
	case errors.Is(err, curseforge.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, curseforge.ErrUnavailable)
	}
	return err
}
