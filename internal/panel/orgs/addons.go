package orgs

import (
	"context"
	"errors"
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
	"github.com/xena-studios/raptor/internal/panel/hangar"
	"github.com/xena-studios/raptor/internal/panel/modrinth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Addons implements AddonService: Modrinth for the plugin installer and
// mod manager, and Hangar for plugins (docs/EGGS.md#add-ons).
type Addons struct {
	Orgs     *Service
	Modrinth *modrinth.Client
	Hangar   *hangar.Client
}

// Add-on sources.
const (
	sourceModrinth = "modrinth"
	sourceHangar   = "hangar"
)

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
	var user string
	err := s.Orgs.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		_, _, err := member(ctx, q, sess, orgID, "member")
		user = idString(sess.UserID)
		return err
	})
	if err != nil {
		return err
	}
	if err := s.Orgs.Auth.RateLimit(ctx, "addons:user:"+user, addonCallsPerMinute, time.Minute); err != nil {
		return err
	}
	if kind != "" {
		if _, ok := eggs.AddonKinds[kind]; !ok {
			return invalid(errors.New("the kind is plugins, mods, or datapacks"))
		}
	}
	if len(loaders) == 0 {
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

func modrinthError(err error) error {
	switch {
	case errors.Is(err, modrinth.ErrNotFound), errors.Is(err, hangar.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, modrinth.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, modrinth.ErrUnavailable)
	case errors.Is(err, hangar.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, hangar.ErrUnavailable)
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
	if source != "" && source != sourceModrinth && source != sourceHangar {
		return invalid(errors.New("the source is modrinth or hangar"))
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

func projectProto(p modrinth.Project) *panelv1.AddonProject {
	return &panelv1.AddonProject{
		Id: p.Key(), Slug: p.Slug, Title: p.Title, Description: p.Description, IconUrl: p.IconURL,
		Downloads: p.Downloads, Author: p.Author, Url: "https://modrinth.com/project/" + p.Slug, Source: sourceModrinth,
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
	projects, total, err := s.Modrinth.Search(ctx, req.GetKind(), req.GetLoaders(), req.GetGameVersion(), query, offset)
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
	if err := s.check(ctx, req.GetOrgId(), "", req.GetLoaders(), req.GetGameVersion()); err != nil {
		return nil, err
	}
	if err := checkSource(req.GetSource()); err != nil {
		return nil, err
	}
	if !projectRef.MatchString(req.GetProjectId()) {
		return nil, invalid(errors.New("that isn't a project"))
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
	return out, nil
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
