package orgs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/curseforge"
	"github.com/xena-studios/raptor/internal/panel/factorio"
	"github.com/xena-studios/raptor/internal/panel/gameversions"
	"github.com/xena-studios/raptor/internal/panel/hangar"
	"github.com/xena-studios/raptor/internal/panel/modpacks"
	"github.com/xena-studios/raptor/internal/panel/modrinth"
	"github.com/xena-studios/raptor/internal/panel/mojang"
	"github.com/xena-studios/raptor/internal/panel/steam"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/panel/umod"
)

// Addons implements AddonService: Modrinth for the plugin installer and
// mod manager, and Hangar for plugins (docs/EGGS.md#add-ons).
type Addons struct {
	Orgs     *Service
	Modrinth *modrinth.Client
	Hangar   *hangar.Client
	Steam    *steam.Client
	UMod     *umod.Client
	Factorio *factorio.Client
	Modpacks *modpacks.Client
	Versions *gameversions.Client
	Mojang   *mojang.Client
	// CurseForge is off (nil, or no key) until the Panel has an API key.
	CurseForge *curseforge.Client
}

// kindModpacks is the pack picker's kind (#413): Modrinth's modpacks,
// which a modpack egg's install script downloads itself.
const kindModpacks = "modpacks"

// Add-on sources.
const (
	sourceModrinth = "modrinth"
	sourceHangar   = "hangar"
	sourceUMod     = "umod"
	sourceFactorio = "factorio"
	// FTB's and Technic's modpacks, for the pack picker only.
	sourceFTB     = "ftb"
	sourceTechnic = "technic"
)

// umodGame is the uMod category the umod kind's plugins come from.
const umodGame = "rust"

var _ panelv1connect.AddonServiceHandler = (*Addons)(nil)

const addonCallsPerMinute = 120

var (
	gameVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z.\-_+]{0,31}$`)
	sha512Hex   = regexp.MustCompile(`^[0-9a-f]{128}$`)
	sha256Hex   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	projectRef  = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,64}$`)
)

// check lets members of the org in, a bounded number of times a minute,
// and checks the kind and loaders.
func (s *Addons) check(ctx context.Context, orgID, kind string, loaders []string, version string) error {
	if err := s.allow(ctx, orgID); err != nil {
		return err
	}
	if kind != "" && kind != kindModpacks {
		if _, ok := eggs.AddonKinds[kind]; !ok {
			return invalid(errors.New("the kind is plugins, mods, or datapacks"))
		}
	}
	// Modpacks bring their own loader, so the picker needn't name one.
	if len(loaders) == 0 && kind != kindModpacks {
		return invalid(errors.New("name the loaders"))
	}
	for _, l := range loaders {
		known := false
		for _, ls := range eggs.AddonKinds {
			known = known || slices.Contains(ls, l)
		}
		if !known {
			return invalid(errors.New("unknown loader " + l))
		}
	}
	if version != "" && !gameVersion.MatchString(version) {
		return invalid(errors.New("that isn't a game version"))
	}
	return nil
}

// allow lets members of the org in, a bounded number of times a minute.
func (s *Addons) allow(ctx context.Context, orgID string) error {
	var user string
	err := s.Orgs.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		_, _, err := member(ctx, q, sess, orgID, "member")
		user = idString(sess.UserID)
		return err
	})
	if err != nil {
		return err
	}
	return s.Orgs.Auth.RateLimit(ctx, "addons:user:"+user, addonCallsPerMinute, time.Minute)
}

func modrinthError(err error) error {
	switch {
	case errors.Is(err, modrinth.ErrNotFound), errors.Is(err, hangar.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, modrinth.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, modrinth.ErrUnavailable)
	case errors.Is(err, hangar.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, hangar.ErrUnavailable)
	case errors.Is(err, steam.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, steam.ErrUnavailable)
	case errors.Is(err, mojang.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, mojang.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, mojang.ErrUnavailable)
	case errors.Is(err, gameversions.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, gameversions.ErrUnavailable)
	case errors.Is(err, umod.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, umod.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, umod.ErrUnavailable)
	case errors.Is(err, modpacks.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, modpacks.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, modpacks.ErrUnavailable)
	case errors.Is(err, factorio.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, factorio.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, factorio.ErrUnavailable)
	}
	return err
}

// hangarPlatform is the Hangar platform for an add-on's loaders, for
// plugins only.
func hangarPlatform(kind string, loaders []string) (string, error) {
	if kind != "" && kind != "plugins" {
		return "", invalid(errors.New("hangar has plugins only"))
	}
	p := hangar.Platforms(loaders)
	if len(p) == 0 {
		return "", invalid(errors.New("hangar has no plugins for these loaders"))
	}
	return p[0], nil
}

func checkSource(source string) error {
	if source != "" && source != sourceModrinth && source != sourceHangar && source != sourceUMod && source != sourceFactorio &&
		source != sourceFTB && source != sourceTechnic && source != sourceCurseForge {
		return invalid(errors.New("the source is modrinth, hangar, curseforge, umod, factorio, ftb, or technic"))
	}
	return nil
}

func hangarProject(p hangar.Project) *panelv1.AddonProject {
	return &panelv1.AddonProject{
		Id: p.Namespace.Slug, Slug: p.Namespace.Slug, Title: p.Name, Description: p.Description, IconUrl: p.AvatarURL,
		Downloads: p.Stats.Downloads, Author: p.Namespace.Owner, Url: p.URL(), Source: sourceHangar,
	}
}

// hangarVersion is a version with a file Wings can install, or nil.
func hangarVersion(project string, v hangar.Version, platform string) *panelv1.AddonVersion {
	d, _, ok := v.File([]string{platform})
	if !ok {
		return nil
	}
	out := &panelv1.AddonVersion{
		Id: strconv.FormatInt(v.ID, 10), ProjectId: project, Name: v.Name, VersionNumber: v.Name,
		GameVersions: v.PlatformDependencies[platform], Loaders: []string{strings.ToLower(platform)},
		File: &panelv1.AddonFile{Url: *d.DownloadURL, Filename: d.FileInfo.Name, Size: d.FileInfo.SizeBytes, Sha256: d.FileInfo.Sha256},
	}
	if !v.CreatedAt.IsZero() {
		out.PublishedAt = timestamppb.New(v.CreatedAt)
	}
	return out
}

func umodProject(p umod.Plugin) *panelv1.AddonProject {
	out := &panelv1.AddonProject{
		Id: p.Slug, Slug: p.Slug, Title: p.Title, Description: p.Description,
		Downloads: p.Downloads, Author: p.Author, Url: "https://umod.org/plugins/" + p.Slug, Source: sourceUMod,
	}
	if u, err := url.Parse(p.IconURL); err == nil && u.Scheme == "https" && u.Host == umod.IconHost {
		out.IconUrl = p.IconURL
	}
	return out
}

// umodVersion is a plugin's one installable file, or nil.
func umodVersion(p umod.Plugin) *panelv1.AddonVersion {
	if !p.Installable() {
		return nil
	}
	out := &panelv1.AddonVersion{
		Id: p.Version, ProjectId: p.Slug, Name: p.Version, VersionNumber: p.Version, Loaders: []string{"oxide", "carbon"},
		File: &panelv1.AddonFile{Url: p.DownloadURL, Filename: p.Name + ".cs", Sha1: p.SHA1},
	}
	if t := p.Released(); !t.IsZero() {
		out.PublishedAt = timestamppb.New(t)
	}
	return out
}

func factorioProject(m factorio.Mod) *panelv1.AddonProject {
	return &panelv1.AddonProject{
		Id: m.Name, Slug: m.Name, Title: cmp.Or(m.Title, m.Name), Description: m.Summary, IconUrl: m.Icon(),
		Downloads: m.Downloads, Author: m.Owner, Url: "https://mods.factorio.com/mod/" + url.PathEscape(m.Name), Source: sourceFactorio,
	}
}

// factorioVersion is a release as an add-on version: its ID is
// "mod@version", and its file is the portal's download, checked by SHA-1.
func factorioVersion(mod string, r factorio.Release) *panelv1.AddonVersion {
	out := &panelv1.AddonVersion{
		Id: mod + "@" + r.Version, ProjectId: mod, Name: r.Version, VersionNumber: r.Version,
		GameVersions: []string{r.Info.FactorioVersion}, Loaders: []string{"factorio"},
		File: &panelv1.AddonFile{Url: factorio.DefaultBase + r.DownloadURL, Filename: r.FileName, Sha1: r.SHA1},
	}
	if !r.Released.IsZero() {
		out.PublishedAt = timestamppb.New(r.Released)
	}
	for _, d := range factorio.Dependencies(r) {
		out.Dependencies = append(out.Dependencies, &panelv1.AddonDependency{ProjectId: d.Name, Type: d.Type})
	}
	return out
}

func projectProto(p modrinth.Project) *panelv1.AddonProject {
	return &panelv1.AddonProject{
		Id: p.Key(), Slug: p.Slug, Title: p.Title, Description: p.Description, IconUrl: p.IconURL,
		Downloads: p.Downloads, Author: p.Author, Url: "https://modrinth.com/project/" + p.Slug, Source: sourceModrinth,
		ServerSide: p.ServerSide, ClientSide: p.ClientSide,
	}
}

func versionProto(v modrinth.Version) *panelv1.AddonVersion {
	out := &panelv1.AddonVersion{
		Id: v.ID, ProjectId: v.ProjectID, Name: v.Name, VersionNumber: v.VersionNumber,
		GameVersions: v.GameVersions, Loaders: v.Loaders,
	}
	if !v.Published.IsZero() {
		out.PublishedAt = timestamppb.New(v.Published)
	}
	if f, ok := v.Main(); ok {
		out.File = &panelv1.AddonFile{Url: f.URL, Filename: f.Filename, Size: f.Size, Sha512: f.Hashes.Sha512}
	}
	for _, d := range v.Dependencies {
		if d.ProjectID != "" {
			out.Dependencies = append(out.Dependencies, &panelv1.AddonDependency{ProjectId: d.ProjectID, VersionId: d.VersionID, Type: d.Type})
		}
	}
	return out
}

// SearchAddons implements AddonService.
func (s *Addons) SearchAddons(ctx context.Context, req *panelv1.SearchAddonsRequest) (*panelv1.SearchAddonsResponse, error) {
	if err := s.check(ctx, req.GetOrgId(), req.GetKind(), req.GetLoaders(), req.GetGameVersion()); err != nil {
		return nil, err
	}
	if err := checkSource(req.GetSource()); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(req.GetQuery())
	if len(query) > 100 {
		query = query[:100]
	}
	offset := int(min(max(req.GetOffset(), 0), 10_000))
	if packSource(req.GetSource()) || (req.GetKind() == kindModpacks && req.GetSource() != "" && req.GetSource() != sourceModrinth) {
		if req.GetKind() != kindModpacks || !packSource(req.GetSource()) {
			return nil, invalid(errors.New("modpacks come from Modrinth, FTB, or Technic"))
		}
		packs, err := s.Modpacks.Search(ctx, req.GetSource(), query)
		if err != nil {
			return nil, modrinthError(err)
		}
		out := &panelv1.SearchAddonsResponse{Total: int32(len(packs))} //nolint:gosec // a page
		if offset > 0 {
			return out, nil // one page
		}
		for _, p := range packs {
			out.Projects = append(out.Projects, &panelv1.AddonProject{
				Id: p.ID, Slug: p.Slug, Title: p.Title, Description: p.Description, IconUrl: p.Icon,
				Downloads: p.Installs, Url: p.URL, Source: req.GetSource(),
			})
		}
		return out, nil
	}
	if req.GetKind() == "factorio" || req.GetSource() == sourceFactorio {
		if req.GetKind() != "factorio" || req.GetSource() != sourceFactorio {
			return nil, invalid(errors.New("factorio's mods come from its mod portal, and only it has them"))
		}
		mods, total, err := s.Factorio.Search(ctx, query, factorio.Major(req.GetGameVersion()), offset)
		if err != nil {
			return nil, modrinthError(err)
		}
		out := &panelv1.SearchAddonsResponse{Total: int32(min(total, 1<<30))} //nolint:gosec // capped
		for _, m := range mods {
			out.Projects = append(out.Projects, factorioProject(m))
		}
		return out, nil
	}
	if req.GetKind() == "umod" || req.GetSource() == sourceUMod {
		if req.GetKind() != "umod" || req.GetSource() != sourceUMod {
			return nil, invalid(errors.New("uMod has the umod kind's plugins, and only uMod has them"))
		}
		plugins, total, err := s.UMod.Search(ctx, umodGame, query, offset/umod.PageSize+1)
		if err != nil {
			return nil, modrinthError(err)
		}
		out := &panelv1.SearchAddonsResponse{Total: int32(min(total, 1<<30))} //nolint:gosec // capped
		for _, p := range plugins {
			out.Projects = append(out.Projects, umodProject(p))
		}
		return out, nil
	}
	if req.GetSource() == sourceCurseForge {
		return s.searchCurseForge(ctx, req, query, offset)
	}
	if req.GetSource() == sourceHangar {
		platform, err := hangarPlatform(req.GetKind(), req.GetLoaders())
		if err != nil {
			return nil, err
		}
		projects, total, err := s.Hangar.Search(ctx, platform, req.GetGameVersion(), query, offset)
		if err != nil {
			return nil, modrinthError(err)
		}
		out := &panelv1.SearchAddonsResponse{Total: int32(min(total, 1<<30))} //nolint:gosec // capped
		for _, p := range projects {
			out.Projects = append(out.Projects, hangarProject(p))
		}
		return out, nil
	}
	projects, total, err := s.Modrinth.Search(ctx, req.GetKind(), req.GetLoaders(), req.GetGameVersion(), query, offset, !req.GetIncludeClientOnly())
	if err != nil {
		return nil, modrinthError(err)
	}
	out := &panelv1.SearchAddonsResponse{Total: int32(min(total, 1<<30))} //nolint:gosec // capped
	for _, p := range projects {
		out.Projects = append(out.Projects, projectProto(p))
	}
	return out, nil
}

// ListAddonVersions implements AddonService.
func (s *Addons) ListAddonVersions(ctx context.Context, req *panelv1.ListAddonVersionsRequest) (*panelv1.ListAddonVersionsResponse, error) {
	if req.GetKind() != "" && req.GetKind() != kindModpacks {
		return nil, invalid(errors.New("the kind is modpacks, or empty"))
	}
	if err := s.check(ctx, req.GetOrgId(), req.GetKind(), req.GetLoaders(), req.GetGameVersion()); err != nil {
		return nil, err
	}
	if err := checkSource(req.GetSource()); err != nil {
		return nil, err
	}
	if packSource(req.GetSource()) {
		if req.GetKind() != kindModpacks || len(req.GetProjectId()) > 100 {
			return nil, invalid(errors.New("modpacks come from Modrinth, FTB, or Technic"))
		}
		vs, err := s.Modpacks.Versions(ctx, req.GetSource(), req.GetProjectId())
		if err != nil {
			return nil, modrinthError(err)
		}
		out := &panelv1.ListAddonVersionsResponse{}
		for _, v := range vs {
			if len(out.Versions) == 50 {
				break
			}
			pv := &panelv1.AddonVersion{Id: v.ID, ProjectId: req.GetProjectId(), Name: v.Name, VersionNumber: v.Name}
			if !v.When.IsZero() && v.When.Unix() > 0 {
				pv.PublishedAt = timestamppb.New(v.When)
			}
			if !v.Stable {
				pv.VersionNumber += " (not a release)"
			}
			out.Versions = append(out.Versions, pv)
		}
		return out, nil
	}
	if req.GetKind() == kindModpacks && req.GetSource() != "" && req.GetSource() != sourceModrinth {
		return nil, invalid(errors.New("modpacks come from Modrinth, FTB, or Technic"))
	}
	if req.GetSource() == sourceFactorio {
		mod, err := s.Factorio.Get(ctx, req.GetProjectId())
		if err != nil {
			return nil, modrinthError(err)
		}
		out := &panelv1.ListAddonVersionsResponse{}
		for _, r := range mod.ReleasesFor(factorio.Major(req.GetGameVersion())) {
			if len(out.Versions) < 50 {
				out.Versions = append(out.Versions, factorioVersion(mod.Name, r))
			}
		}
		return out, nil
	}
	if !projectRef.MatchString(req.GetProjectId()) {
		return nil, invalid(errors.New("that isn't a project"))
	}
	if req.GetSource() == sourceCurseForge {
		return s.curseForgeVersions(ctx, req)
	}
	if req.GetSource() == sourceUMod {
		p, err := s.UMod.Get(ctx, req.GetProjectId())
		if err != nil {
			return nil, modrinthError(err)
		}
		out := &panelv1.ListAddonVersionsResponse{}
		if v := umodVersion(p); v != nil {
			out.Versions = append(out.Versions, v)
		}
		return out, nil
	}
	if req.GetSource() == sourceHangar {
		platform, err := hangarPlatform("", req.GetLoaders())
		if err != nil {
			return nil, err
		}
		versions, err := s.Hangar.Versions(ctx, req.GetProjectId(), platform, req.GetGameVersion())
		if err != nil {
			return nil, modrinthError(err)
		}
		out := &panelv1.ListAddonVersionsResponse{}
		for _, v := range versions {
			if pv := hangarVersion(req.GetProjectId(), v, platform); pv != nil {
				out.Versions = append(out.Versions, pv)
			}
		}
		return out, nil
	}
	versions, err := s.Modrinth.Versions(ctx, req.GetProjectId(), req.GetLoaders(), req.GetGameVersion())
	if err != nil {
		return nil, modrinthError(err)
	}
	out := &panelv1.ListAddonVersionsResponse{}
	for _, v := range versions {
		if _, ok := v.Main(); ok && len(out.Versions) < 50 {
			out.Versions = append(out.Versions, versionProto(v))
		}
	}
	return out, nil
}

// IdentifyAddons implements AddonService.
func (s *Addons) IdentifyAddons(ctx context.Context, req *panelv1.IdentifyAddonsRequest) (*panelv1.IdentifyAddonsResponse, error) {
	if err := s.check(ctx, req.GetOrgId(), "", req.GetLoaders(), req.GetGameVersion()); err != nil {
		return nil, err
	}
	hashes := req.GetHashes()
	if len(hashes) > modrinth.MaxHashes {
		return nil, invalid(errors.New("too many files at once"))
	}
	for _, h := range hashes {
		if !sha512Hex.MatchString(h) {
			return nil, invalid(errors.New("that isn't a SHA-512"))
		}
	}
	for k, v := range req.GetSha256() {
		if !sha512Hex.MatchString(k) || !sha256Hex.MatchString(v) {
			return nil, invalid(errors.New("that isn't a SHA-256"))
		}
	}
	for k, v := range req.GetSha1() {
		if !sha512Hex.MatchString(k) || !sha1Hex.MatchString(v) {
			return nil, invalid(errors.New("that isn't a SHA-1"))
		}
	}
	for k := range req.GetCurseforge() {
		if !sha512Hex.MatchString(k) {
			return nil, invalid(errors.New("that isn't a SHA-512"))
		}
	}
	for k, v := range req.GetNames() {
		if !sha512Hex.MatchString(k) || v == "" || len(v) > 255 || strings.ContainsAny(v, `/\`) {
			return nil, invalid(errors.New("that isn't a file name"))
		}
	}
	out := &panelv1.IdentifyAddonsResponse{}
	if len(hashes) == 0 {
		return out, nil
	}
	current, err := s.Modrinth.ByHash(ctx, hashes)
	if err != nil {
		return nil, modrinthError(err)
	}
	latest, err := s.Modrinth.Latest(ctx, hashes, req.GetLoaders(), req.GetGameVersion())
	if err != nil && !errors.Is(err, modrinth.ErrNotFound) {
		return nil, modrinthError(err)
	}
	var ids []string
	for _, v := range current {
		if !slices.Contains(ids, v.ProjectID) {
			ids = append(ids, v.ProjectID)
		}
	}
	projects := map[string]modrinth.Project{}
	if len(ids) > 0 {
		list, err := s.Modrinth.Projects(ctx, ids)
		if err != nil {
			return nil, modrinthError(err)
		}
		for _, p := range list {
			projects[p.Key()] = p
		}
	}
	for _, h := range hashes {
		v, ok := current[h]
		if !ok {
			continue
		}
		a := &panelv1.IdentifiedAddon{Sha512: h, Version: versionProto(v)}
		if p, ok := projects[v.ProjectID]; ok {
			a.Project = projectProto(p)
		}
		if l, ok := latest[h]; ok && l.ID != v.ID && l.Published.After(v.Published) {
			a.Latest = versionProto(l)
		}
		out.Addons = append(out.Addons, a)
	}
	if err := s.identifyOnHangar(ctx, req, current, out); err != nil {
		return nil, err
	}
	if err := s.identifyOnCurseForge(ctx, req, out); err != nil {
		return nil, err
	}
	if err := s.identifyOnUMod(ctx, req, out); err != nil {
		return nil, err
	}
	if err := s.identifyOnFactorio(ctx, req, out); err != nil {
		return nil, err
	}
	return out, nil
}

var sha1Hex = regexp.MustCompile(`^[0-9a-f]{40}$`)

// identifyOnUMod names uMod plugins by their files' names (uMod has no
// lookup by hash): Kits.cs is the plugin named Kits. A file whose SHA-1
// is the newest file's is that version; any other is offered the newest,
// since uMod lists no other. At most umod.MaxNames are looked up.
func (s *Addons) identifyOnUMod(ctx context.Context, req *panelv1.IdentifyAddonsRequest, out *panelv1.IdentifyAddonsResponse) error {
	if s.UMod == nil || !slices.ContainsFunc(req.GetLoaders(), func(l string) bool { return l == "oxide" || l == "carbon" }) {
		return nil
	}
	looked := 0
	for _, h := range req.GetHashes() {
		file := req.GetNames()[h]
		pluginName, ok := strings.CutSuffix(file, ".cs")
		if !ok || looked == umod.MaxNames {
			continue
		}
		looked++
		p, found, err := s.UMod.ByName(ctx, umodGame, pluginName)
		if err != nil {
			return modrinthError(err)
		}
		v := umodVersion(p)
		if !found || v == nil {
			continue
		}
		a := &panelv1.IdentifiedAddon{Sha512: h, Project: umodProject(p)}
		if req.GetSha1()[h] == p.SHA1 {
			a.Version = v
		} else {
			a.Latest = v
		}
		out.Addons = append(out.Addons, a)
	}
	return nil
}

// identifyOnHangar looks up on Hangar, by SHA-256, the files Modrinth
// doesn't know. Hangar answers one file at a time, so at most
// hangar.MaxHashes are looked up.
func (s *Addons) identifyOnHangar(ctx context.Context, req *panelv1.IdentifyAddonsRequest, known map[string]modrinth.Version, out *panelv1.IdentifyAddonsResponse) error {
	platform := ""
	if p := hangar.Platforms(req.GetLoaders()); len(p) > 0 {
		platform = p[0]
	}
	if s.Hangar == nil || platform == "" {
		return nil
	}
	looked := 0
	for _, h := range req.GetHashes() {
		sum, ok := req.GetSha256()[h]
		if _, found := known[h]; found || !ok || looked == hangar.MaxHashes {
			continue
		}
		looked++
		p, err := s.Hangar.ByHash(ctx, sum)
		if errors.Is(err, hangar.ErrNotFound) {
			continue
		}
		if err != nil {
			return modrinthError(err)
		}
		a := &panelv1.IdentifiedAddon{Sha512: h, Project: hangarProject(p)}
		versions, err := s.Hangar.Versions(ctx, p.Namespace.Slug, platform, req.GetGameVersion())
		if err != nil && !errors.Is(err, hangar.ErrNotFound) {
			return modrinthError(err)
		}
		// The newest version is offered only when this file is one of
		// the versions listed, so an update is never a step back.
		var newest *panelv1.AddonVersion
		for _, v := range versions {
			pv := hangarVersion(p.Namespace.Slug, v, platform)
			if pv == nil {
				continue
			}
			if pv.GetFile().GetSha256() == sum {
				a.Version = pv
				if newest != nil {
					a.Latest = newest
				}
				break
			}
			if newest == nil {
				newest = pv
			}
		}
		out.Addons = append(out.Addons, a)
	}
	return nil
}

// workshopID is a Steam Workshop item's ID.
var workshopID = regexp.MustCompile(`^[0-9]{1,20}$`)

// LookupWorkshopItems names Steam Workshop items and collections.
func (s *Addons) LookupWorkshopItems(ctx context.Context, req *panelv1.LookupWorkshopItemsRequest) (*panelv1.LookupWorkshopItemsResponse, error) {
	if err := s.allow(ctx, req.GetOrgId()); err != nil {
		return nil, err
	}
	ids := req.GetIds()
	if len(ids) > steam.MaxItems {
		return nil, invalid(fmt.Errorf("at most %d items at a time", steam.MaxItems))
	}
	for _, id := range ids {
		if !workshopID.MatchString(id) {
			return nil, invalid(errors.New("a Workshop ID is the number at the end of its link"))
		}
	}
	items, err := s.Steam.Items(ctx, ids)
	if err != nil {
		return nil, modrinthError(err)
	}
	out := &panelv1.LookupWorkshopItemsResponse{}
	for _, id := range ids {
		it, ok := items[id]
		if !ok {
			continue
		}
		delete(items, id) // once, if it's asked for twice
		out.Items = append(out.Items, &panelv1.WorkshopItem{
			Id: it.ID, AppId: it.AppID, Title: it.Title, PreviewUrl: it.Preview, Size: it.Size,
			UpdatedAt: timestamppb.New(it.Updated), Collection: it.Collection, Children: it.Children,
		})
	}
	return out, nil
}

// identifyOnFactorio names Factorio mods by their files' names
// (Krastorio2_2.1.4.zip is Krastorio2 2.1.4): the release with that
// version, checked by SHA-1, and the newest release for the server's
// Factorio version when it's newer. At most factorio.MaxNames are looked up.
func (s *Addons) identifyOnFactorio(ctx context.Context, req *panelv1.IdentifyAddonsRequest, out *panelv1.IdentifyAddonsResponse) error {
	if s.Factorio == nil || !slices.Contains(req.GetLoaders(), "factorio") {
		return nil
	}
	major := factorio.Major(req.GetGameVersion())
	looked := 0
	for _, h := range req.GetHashes() {
		name, version, ok := factorio.ParseFile(req.GetNames()[h])
		if !ok || looked == factorio.MaxNames {
			continue
		}
		looked++
		mod, err := s.Factorio.Get(ctx, name)
		if errors.Is(err, factorio.ErrNotFound) {
			continue
		}
		if err != nil {
			return modrinthError(err)
		}
		a := &panelv1.IdentifiedAddon{Sha512: h, Project: factorioProject(mod.Mod)}
		for _, r := range mod.Releases {
			if r.Version == version && r.SHA1 == req.GetSha1()[h] {
				a.Version = factorioVersion(mod.Name, r)
			}
		}
		if newest := mod.ReleasesFor(major); len(newest) > 0 && newest[0].Version != version {
			if a.Version == nil || newest[0].Released.After(a.GetVersion().GetPublishedAt().AsTime()) {
				a.Latest = factorioVersion(mod.Name, newest[0])
			}
		}
		out.Addons = append(out.Addons, a)
	}
	return nil
}

// ListGameVersions implements AddonService (#420).
func (s *Addons) ListGameVersions(ctx context.Context, req *panelv1.ListGameVersionsRequest) (*panelv1.ListGameVersionsResponse, error) {
	if err := s.allow(ctx, req.GetOrgId()); err != nil {
		return nil, err
	}
	if !slices.Contains(gameversions.Sources, req.GetSource()) {
		return nil, invalid(errors.New("unknown game software"))
	}
	list, err := s.Versions.List(ctx, req.GetSource())
	if err != nil {
		return nil, modrinthError(err)
	}
	out := &panelv1.ListGameVersionsResponse{}
	for _, v := range list {
		if len(out.Versions) == 500 {
			break
		}
		out.Versions = append(out.Versions, &panelv1.GameVersion{Id: v.ID, Stable: v.Stable})
	}
	return out, nil
}

// LookupMinecraftPlayer implements AddonService (#422).
func (s *Addons) LookupMinecraftPlayer(ctx context.Context, req *panelv1.LookupMinecraftPlayerRequest) (*panelv1.LookupMinecraftPlayerResponse, error) {
	if err := s.allow(ctx, req.GetOrgId()); err != nil {
		return nil, err
	}
	if !mojang.ValidName(req.GetName()) {
		return nil, invalid(errors.New("a Minecraft name is 1 to 16 letters, digits, and underscores"))
	}
	if req.GetOffline() {
		return &panelv1.LookupMinecraftPlayerResponse{Name: req.GetName(), Uuid: mojang.OfflineUUID(req.GetName())}, nil
	}
	p, err := s.Mojang.Lookup(ctx, req.GetName())
	if err != nil {
		return nil, modrinthError(err)
	}
	return &panelv1.LookupMinecraftPlayerResponse{Name: p.Name, Uuid: p.UUID}, nil
}

// packSource reports whether a source is one only the pack picker uses.
func packSource(s string) bool { return s == sourceFTB || s == sourceTechnic }
