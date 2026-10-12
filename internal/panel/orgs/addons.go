package orgs

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/modrinth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Addons implements AddonService: Modrinth, for the plugin installer and
// mod manager (docs/EGGS.md#add-ons).
type Addons struct {
	Orgs     *Service
	Modrinth *modrinth.Client
}

var _ panelv1connect.AddonServiceHandler = (*Addons)(nil)

const addonCallsPerMinute = 120

var (
	gameVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z.\-_+]{0,31}$`)
	sha512Hex   = regexp.MustCompile(`^[0-9a-f]{128}$`)
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
			return invalid(errors.New("the kind is plugins or mods"))
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
	case errors.Is(err, modrinth.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, modrinth.ErrUnavailable):
		return connect.NewError(connect.CodeUnavailable, modrinth.ErrUnavailable)
	}
	return err
}

func projectProto(p modrinth.Project) *panelv1.AddonProject {
	return &panelv1.AddonProject{
		Id: p.Key(), Slug: p.Slug, Title: p.Title, Description: p.Description, IconUrl: p.IconURL,
		Downloads: p.Downloads, Author: p.Author, Url: "https://modrinth.com/project/" + p.Slug,
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
	query := strings.TrimSpace(req.GetQuery())
	if len(query) > 100 {
		query = query[:100]
	}
	projects, total, err := s.Modrinth.Search(ctx, req.GetKind(), req.GetLoaders(), req.GetGameVersion(), query, int(min(max(req.GetOffset(), 0), 10_000)))
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
	if !projectRef.MatchString(req.GetProjectId()) {
		return nil, invalid(errors.New("that isn't a Modrinth project"))
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
	return out, nil
}
