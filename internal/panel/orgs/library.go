package orgs

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/eggs/spec"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/catalog"
	"github.com/xena-studios/raptor/internal/panel/eggimport"
	"github.com/xena-studios/raptor/internal/panel/staff"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Eggs implements EggService, the egg library (docs/PANEL.md#eggs): an
// org's own eggs, Raptor's official ones (org_id NULL, changed by staff
// admins; Raptor's catalog is seeded into them), and those other orgs
// share.
type Eggs struct {
	Orgs *Service
	// Staff checks staff for the official library (nil: there's none).
	Staff *staff.Service
}

var _ panelv1connect.EggServiceHandler = (*Eggs)(nil)

// Library limits.
const (
	// MaxOfficialEggs is how many official eggs there can be.
	MaxOfficialEggs = 500
	maxSummary      = 160
	maxNote         = 200
)

// Categories are what an egg can be filed under.
var Categories = []string{"minecraft", "steam", "standalone", "software", "database", "voice", "generic", "other"}

var (
	errNoEgg      = connect.NewError(connect.CodeNotFound, errors.New("that egg isn't in this library; it may have been deleted"))
	errNotOfficer = connect.NewError(connect.CodeNotFound, errors.New("not found"))
	errNotLive    = connect.NewError(connect.CodeFailedPrecondition, errors.New("only published eggs can be deployed; publish it first"))
)

// lib is the library a call is about, and who's calling.
type lib struct {
	official bool
	org      pgtype.UUID
	sess     *auth.Session
	actor    *staff.Actor
	q        *store.Queries
}

func (l *lib) email() string {
	if l.actor != nil {
		return l.actor.Email()
	}
	return l.sess.User.Email
}

func (l *lib) userID() pgtype.UUID {
	if l.actor != nil {
		return l.actor.UserID()
	}
	return l.sess.UserID
}

// owns reports whether an egg is in this library.
func (l *lib) owns(e store.Egg) bool {
	if l.official {
		return !e.OrgID.Valid
	}
	return e.OrgID == l.org
}

// sees reports whether this library can use an egg: its own, a published
// official one, or one another org shares (staff see removed ones too).
func (l *lib) sees(e store.Egg) bool {
	switch {
	case l.owns(e), !e.OrgID.Valid && e.Status == "published":
		return true
	case e.SharedAt.Valid && l.official:
		return true
	}
	return e.SharedAt.Valid && !e.RemovedAt.Valid && e.Status == "published"
}

// tx runs fn for the library orgID names: as the user under row-level
// security for an org (members to look, admins and owners to change), or
// on the Panel's own connection for staff (the official library).
func (s *Eggs) tx(ctx context.Context, orgID string, change bool, fn func(*lib) error) error {
	if orgID == "" {
		if s.Staff == nil {
			return errNotOfficer
		}
		a, err := s.Staff.Require(ctx, change)
		if err != nil {
			return err
		}
		return pgx.BeginFunc(ctx, s.Orgs.DB, func(tx pgx.Tx) error {
			return fn(&lib{official: true, actor: a, q: store.New(tx)})
		})
	}
	return s.Orgs.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		min := "member"
		if change {
			min = "admin"
		}
		org, _, err := member(ctx, q, sess, orgID, min)
		if err != nil {
			return err
		}
		return fn(&lib{org: org, sess: sess, q: q})
	})
}

func (s *Eggs) audit(ctx context.Context, l *lib, action, target string, meta map[string]any) error {
	if l.official {
		return s.Staff.Record(ctx, l.actor, action, "egg", target, meta)
	}
	return s.Orgs.audit(ctx, l.q, l.sess, l.org, action, target, nil, meta)
}

// eggID parses a library egg's ID ("org:" and the ID, as servers made
// before the library name them, works too). ok is false for anything else,
// such as a catalog ID ("minecraft/paper"), which load looks up.
func eggID(id string) (pgtype.UUID, bool) {
	u, err := uuid.Parse(strings.TrimPrefix(id, orgEggPrefix))
	if err != nil {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: u, Valid: true}, true
}

// counts is how many servers were made from each egg: the org's, or
// everyone's for the official library.
func (s *Eggs) counts(ctx context.Context, l *lib) (map[string]int32, error) {
	out := map[string]int32{}
	q := store.New(s.Orgs.DB)
	if l.official {
		rows, err := q.EggServerCounts(ctx)
		for _, r := range rows {
			out[r.Egg] = r.N
		}
		return out, err
	}
	rows, err := q.OrgEggServerCounts(ctx, l.org)
	for _, r := range rows {
		out[r.Egg] = r.N
	}
	return out, err
}

// described caches what an egg file says (catalog.Proto), by SHA-256:
// files never change, and lists show the same official eggs to everyone.
var described sync.Map

// describeEgg is a library egg as the web app lists it. An egg that no
// longer parses (the parser got stricter) returns an error.
func describeEgg(l *lib, e store.Egg, createdBy string) (*panelv1.CatalogEgg, error) {
	cached, ok := described.Load(e.Sha256)
	if !ok {
		parsed, err := eggs.Parse(e.Egg)
		if err != nil {
			return nil, err
		}
		cached, _ = described.LoadOrStore(e.Sha256, catalog.Proto(parsed))
	}
	p := proto.CloneOf(cached.(*panelv1.CatalogEgg))
	p.Certified = e.Certified
	p.Id, p.Name, p.Summary, p.Category, p.SourceUrl = idString(e.ID), e.Name, e.Summary, e.Category, e.SourceUrl
	p.Scope, p.Origin, p.Status, p.Version = "org", e.Origin, e.Status, e.Version
	switch {
	case !e.OrgID.Valid:
		p.Scope = "official"
	case l.official || e.OrgID != l.org:
		p.Scope = "community"
	}
	p.SharedAt, p.Removed = ts(e.SharedAt), e.RemovedAt.Valid
	if p.Removed && (l.official || e.OrgID == l.org) {
		p.RemovedReason = e.RemovedReason
	}
	p.ParentId, p.ParentName, p.UpdatedAt, p.CreatedBy = e.ParentID, e.ParentName, ts(e.UpdatedAt), createdBy
	return p, nil
}

// servers is how many servers were made from an egg: by its ID, or by its
// catalog ID for servers made before the catalog was in the library.
func servers(counts map[string]int32, e store.Egg) int32 {
	n := counts[idString(e.ID)]
	if e.CatalogID.Valid {
		n += counts[e.CatalogID.String]
	}
	return n
}

// ListEggs implements EggService.
func (s *Eggs) ListEggs(ctx context.Context, req *panelv1.EggServiceListEggsRequest) (*panelv1.EggServiceListEggsResponse, error) {
	out := &panelv1.EggServiceListEggsResponse{}
	var l *lib
	err := s.tx(ctx, req.GetOrgId(), false, func(lb *lib) error {
		l = lb
		type row struct {
			egg   store.Egg
			email pgtype.Text
		}
		var rows []row
		if l.official {
			list, err := l.q.ListOfficialEggs(ctx)
			if err != nil {
				return err
			}
			for _, r := range list {
				rows = append(rows, row{r.Egg, r.CreatedByEmail})
			}
		} else {
			list, err := l.q.ListLibraryEggs(ctx, l.org)
			if err != nil {
				return err
			}
			for _, r := range list {
				rows = append(rows, row{r.Egg, r.CreatedByEmail})
			}
		}
		counts, err := s.counts(ctx, l)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if p, err := describeEgg(l, r.egg, r.email.String); err == nil {
				p.Servers = servers(counts, r.egg)
				out.Eggs = append(out.Eggs, p)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// resolve turns an egg's ID into its row's: a library ID as it is, or a
// catalog ID ("minecraft/paper", as servers made before the catalog was in
// the library name it) looked up.
func resolve(ctx context.Context, l *lib, id string) (pgtype.UUID, error) {
	if u, ok := eggID(id); ok {
		return u, nil
	}
	r, err := l.q.GetEggByCatalogID(ctx, pgtype.Text{String: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, errNoEgg
	}
	return r.ID, err
}

// load finds an egg this library can see.
func (s *Eggs) load(ctx context.Context, l *lib, id string) (*store.Egg, string, error) {
	u, err := resolve(ctx, l, id)
	if err != nil {
		return nil, "", err
	}
	r, err := l.q.GetEgg(ctx, u)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !l.sees(r.Egg)) {
		return nil, "", errNoEgg
	}
	if err != nil {
		return nil, "", err
	}
	return &r.Egg, r.CreatedByEmail.String, nil
}

func toSpec(p *panelv1.EggSpec) *spec.Spec {
	s := &spec.Spec{
		Name: p.GetName(), Author: p.GetAuthor(), Description: p.GetDescription(), StopType: p.GetStopType(),
		StopValue: p.GetStopValue(), Done: p.GetDone(),
		Install:  eggs.Install{Script: p.GetInstallScript(), Container: p.GetInstallContainer(), Entrypoint: p.GetInstallEntrypoint()},
		Features: p.GetFeatures(), FileDenylist: p.GetFileDenylist(), Arch: p.GetArch(),
		PlayersQuery: p.GetPlayersQuery(), PlayersPort: p.GetPlayersPort(),
	}
	for _, i := range p.GetImages() {
		s.Images = append(s.Images, eggs.Image{Name: i.GetName(), Ref: i.GetRef()})
	}
	for _, c := range p.GetStartup() {
		s.Startup = append(s.Startup, eggs.StartupCommand{Name: c.GetName(), Command: c.GetCommand()})
	}
	for _, v := range p.GetVariables() {
		s.Variables = append(s.Variables, eggs.Variable{
			Name: v.GetName(), Description: v.GetDescription(), Env: v.GetEnv(), Default: v.GetDefault(),
			UserViewable: v.GetUserViewable(), UserEditable: v.GetUserEditable(), Rules: v.GetRules(),
		})
	}
	for _, a := range p.GetAddons() {
		if s.Addons == nil {
			s.Addons = map[string]eggs.Addon{}
		}
		s.Addons[a.GetKind()] = eggs.Addon{Loaders: a.GetLoaders(), Dir: a.GetDir(), Version: a.GetVersion()}
	}
	for _, f := range p.GetFiles() {
		cf := spec.ConfigFile{Path: f.GetPath(), Parser: f.GetParser()}
		for _, r := range f.GetFind() {
			cf.Find = append(cf.Find, spec.Find{Key: r.GetKey(), IfValue: r.GetIfValue(), Value: r.GetValue()})
		}
		s.Files = append(s.Files, cf)
	}
	return s
}

func specProto(s *spec.Spec) *panelv1.EggSpec {
	p := &panelv1.EggSpec{
		Name: s.Name, Author: s.Author, Description: s.Description, StopType: s.StopType, StopValue: s.StopValue,
		Done: s.Done, InstallScript: s.Install.Script, InstallContainer: s.Install.Container,
		InstallEntrypoint: s.Install.Entrypoint, Features: s.Features, FileDenylist: s.FileDenylist, Arch: s.Arch,
		PlayersQuery: s.PlayersQuery, PlayersPort: s.PlayersPort,
	}
	for _, i := range s.Images {
		p.Images = append(p.Images, &panelv1.EggImage{Name: i.Name, Ref: i.Ref})
	}
	for _, c := range s.Startup {
		p.Startup = append(p.Startup, &panelv1.EggStartup{Name: c.Name, Command: c.Command})
	}
	for _, v := range s.Variables {
		p.Variables = append(p.Variables, &panelv1.EggVariable{
			Name: v.Name, Description: v.Description, Env: v.Env, Default: v.Default,
			UserViewable: v.UserViewable, UserEditable: v.UserEditable, Rules: v.Rules,
		})
	}
	for _, kind := range slices.Sorted(maps.Keys(s.Addons)) {
		a := s.Addons[kind]
		p.Addons = append(p.Addons, &panelv1.EggAddon{Kind: kind, Loaders: a.Loaders, Dir: a.Dir, Version: a.Version})
	}
	for _, f := range s.Files {
		cf := &panelv1.EggConfigFile{Path: f.Path, Parser: f.Parser}
		for _, r := range f.Find {
			cf.Find = append(cf.Find, &panelv1.EggFind{Key: r.Key, IfValue: r.IfValue, Value: r.Value})
		}
		p.Files = append(p.Files, cf)
	}
	return p
}

// GetEggDetails implements EggService.
func (s *Eggs) GetEggDetails(ctx context.Context, req *panelv1.GetEggDetailsRequest) (*panelv1.GetEggDetailsResponse, error) {
	out := &panelv1.GetEggDetailsResponse{}
	var l *lib
	err := s.tx(ctx, req.GetOrgId(), false, func(lb *lib) error {
		l = lb
		row, email, err := s.load(ctx, l, req.GetId())
		if err != nil {
			return err
		}
		if out.Egg, err = describeEgg(l, *row, email); err != nil {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("this egg no longer reads as an egg; its file has to be fixed"))
		}
		out.File = row.Egg
		out.CanEdit = l.owns(*row) && s.canChange(ctx, l)
		if pub, err := store.New(s.Orgs.DB).EggPublisher(ctx, row.ID); err == nil {
			out.Egg.Copies = pub.Copies
			if out.Egg.GetScope() == "community" {
				out.Egg.Publisher = pub.Name.String
			}
		}
		counts, err := s.counts(ctx, l)
		if err != nil {
			return err
		}
		out.Egg.Servers = servers(counts, *row)
		versions, err := l.q.ListEggVersions(ctx, row.ID)
		for _, v := range versions {
			out.Versions = append(out.Versions, &panelv1.EggVersion{
				Version: v.Version, Sha256: v.Sha256, Note: v.Note, CreatedBy: v.CreatedBy, CreatedAt: ts(v.CreatedAt),
			})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	sp, err := spec.Read(out.File)
	if err != nil {
		return nil, err
	}
	out.Spec = specProto(sp)
	if r, err := eggimport.Parse(out.File); err == nil {
		out.Registries = r.Registries
		for _, w := range r.Warnings {
			out.Warnings = append(out.Warnings, &panelv1.EggWarning{Kind: w.Kind, Text: w.Text})
		}
		out.Format = format(r.Egg)
		out.Lint = r.Egg.Lint()
	}
	return out, nil
}

func format(e *eggs.Egg) string {
	if strings.HasPrefix(e.Format, "PLCN") {
		return "pelican"
	}
	return "pterodactyl"
}

// canChange reports whether the caller may change the library's eggs.
func (s *Eggs) canChange(ctx context.Context, l *lib) bool {
	if l.official {
		return l.actor.Admin()
	}
	m, err := l.q.OrgMember(ctx, store.OrgMemberParams{OrgID: l.org, UserID: l.sess.UserID})
	return err == nil && roleRank[m.Role] >= roleRank["admin"]
}

// GetEggFile implements EggService.
func (s *Eggs) GetEggFile(ctx context.Context, req *panelv1.GetEggFileRequest) (*panelv1.GetEggFileResponse, error) {
	if req.GetOrgId() == "" {
		return nil, errNoEgg
	}
	out := &panelv1.GetEggFileResponse{}
	err := s.tx(ctx, req.GetOrgId(), false, func(l *lib) error {
		row, _, err := s.load(ctx, l, req.GetId())
		if err != nil {
			return err
		}
		if row.Status != "published" {
			return errNotLive
		}
		out.Egg, out.Version = row.Egg, row.Version
		if v := req.GetVersion(); v > 0 && v != row.Version {
			old, err := l.q.GetEggVersion(ctx, store.GetEggVersionParams{EggID: row.ID, Version: v})
			if errors.Is(err, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, fmt.Errorf("this egg has no version %d", v))
			}
			if err != nil {
				return err
			}
			out.Egg, out.Version = old.Egg, old.Version
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func checkMeta(summary, category string) (string, string, error) {
	summary = strings.TrimSpace(summary)
	if utf8.RuneCountInString(summary) > maxSummary {
		return "", "", invalid(fmt.Errorf("the summary is at most %d characters", maxSummary))
	}
	if category == "" {
		category = "other"
	}
	if !slices.Contains(Categories, category) {
		return "", "", invalid(errors.New("choose one of the categories"))
	}
	return summary, category, nil
}

// checkFile checks an egg file a library will keep, and returns its name.
func checkFile(data []byte) (string, error) {
	r, err := eggimport.Parse(data)
	if err != nil {
		return "", invalid(err)
	}
	return eggName(r.Egg.Name), nil
}

func specError(err error) error {
	if spec.IsInvalid(err) {
		return invalid(err)
	}
	return err
}

func (s *Eggs) room(ctx context.Context, l *lib) error {
	if l.official {
		n, err := l.q.CountOfficialEggs(ctx)
		if err == nil && n >= MaxOfficialEggs {
			return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("there can be up to %d official eggs", MaxOfficialEggs))
		}
		return err
	}
	n, err := l.q.CountOrgEggs(ctx, l.org)
	if err == nil && n >= MaxOrgEggs {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("an org can have up to %d eggs; delete one you don't use first", MaxOrgEggs))
	}
	return err
}

// create saves a new egg and its first version.
func (s *Eggs) create(ctx context.Context, l *lib, p store.CreateEggParams, note string) (store.Egg, error) {
	if err := s.room(ctx, l); err != nil {
		return store.Egg{}, err
	}
	if !l.official {
		p.OrgID = l.org
	}
	p.Sha256, p.CreatedBy = eggimport.Sum(p.Egg), l.userID()
	row, err := l.q.CreateEgg(ctx, p)
	if err != nil {
		return row, err
	}
	err = l.q.AddEggVersion(ctx, store.AddEggVersionParams{
		EggID: row.ID, Version: 1, Egg: row.Egg, Sha256: row.Sha256, Note: note, CreatedBy: l.email(),
	})
	return row, err
}

// CreateEgg implements EggService.
func (s *Eggs) CreateEgg(ctx context.Context, req *panelv1.CreateEggRequest) (*panelv1.CreateEggResponse, error) {
	summary, category, err := checkMeta(req.GetSummary(), req.GetCategory())
	if err != nil {
		return nil, err
	}
	file, err := spec.Write(spec.Blank(time.Now()), toSpec(req.GetSpec()))
	if err != nil {
		return nil, specError(err)
	}
	name, err := checkFile(file)
	if err != nil {
		return nil, err
	}
	out := &panelv1.CreateEggResponse{}
	err = s.tx(ctx, req.GetOrgId(), true, func(l *lib) error {
		row, err := s.create(ctx, l, store.CreateEggParams{
			Name: name, Summary: summary, Category: category, Origin: "scratch", Status: "draft", Egg: file,
		}, "Created")
		if err != nil {
			return err
		}
		out.Id = idString(row.ID)
		return s.audit(ctx, l, "egg.create", out.Id, map[string]any{"name": name})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// editable loads an egg in this library to change, locked.
func editable(ctx context.Context, l *lib, id string) (store.Egg, error) {
	u, err := resolve(ctx, l, id)
	if err != nil {
		return store.Egg{}, err
	}
	row, err := l.q.GetEggForUpdate(ctx, u)
	if err == nil && l.owns(row) {
		return row, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return row, err
	}
	// Locking needs the right to change it, so an official egg an org
	// sees doesn't lock: say why it can't be changed.
	if seen, err := l.q.GetEgg(ctx, u); err == nil && !seen.Egg.OrgID.Valid && !l.official {
		return row, connect.NewError(connect.CodeFailedPrecondition, errors.New("official eggs are Raptor's; customize one to make your own copy"))
	}
	return row, errNoEgg
}

// UpdateEgg implements EggService.
func (s *Eggs) UpdateEgg(ctx context.Context, req *panelv1.UpdateEggRequest) (*panelv1.UpdateEggResponse, error) {
	summary, category, err := checkMeta(req.GetSummary(), req.GetCategory())
	if err != nil {
		return nil, err
	}
	note := strings.TrimSpace(req.GetNote())
	if utf8.RuneCountInString(note) > maxNote {
		return nil, invalid(fmt.Errorf("the note is at most %d characters", maxNote))
	}
	out := &panelv1.UpdateEggResponse{}
	err = s.tx(ctx, req.GetOrgId(), true, func(l *lib) error {
		row, err := editable(ctx, l, req.GetId())
		if err != nil {
			return err
		}
		if b := req.GetBaseVersion(); b != 0 && b != row.Version {
			return connect.NewError(connect.CodeAborted, fmt.Errorf("someone saved version %d since you opened this; reload to see it", row.Version))
		}
		file := row.Egg
		switch c := req.GetChange().(type) {
		case *panelv1.UpdateEggRequest_Spec:
			if file, err = spec.Write(row.Egg, toSpec(c.Spec)); err != nil {
				return specError(err)
			}
		case *panelv1.UpdateEggRequest_File:
			file = c.File
			if req.GetKeepRaptor() {
				if file, err = spec.KeepRaptor(c.File, row.Egg); err != nil {
					return specError(err)
				}
			}
		}
		source := req.GetSourceUrl()
		if source != "" {
			if _, err := eggimport.ParseURL(source); err != nil {
				return invalid(err)
			}
		}
		name, err := checkFile(file)
		if err != nil {
			return err
		}
		sum := eggimport.Sum(file)
		version := row.Version
		if sum != row.Sha256 {
			version++
			if note == "" {
				note = "Edited"
			}
			if err := l.q.AddEggVersion(ctx, store.AddEggVersionParams{
				EggID: row.ID, Version: version, Egg: file, Sha256: sum, Note: note, CreatedBy: l.email(),
			}); err != nil {
				return err
			}
		} else if summary == row.Summary && category == row.Category && (source == "" || source == row.SourceUrl) {
			out.Version = version
			return nil
		}
		if source != "" && source != row.SourceUrl {
			if err := l.q.SetEggSource(ctx, store.SetEggSourceParams{SourceUrl: source, ID: row.ID}); err != nil {
				return err
			}
		}
		// An edited catalog egg is no longer the file the conformance
		// suite tests.
		if row.Certified && sum != row.Sha256 {
			if err := l.q.ClearEggCertified(ctx, row.ID); err != nil {
				return err
			}
		}
		if err := l.q.UpdateEgg(ctx, store.UpdateEggParams{
			ID: row.ID, Name: name, Summary: summary, Category: category, Egg: file, Sha256: sum, Version: version,
		}); err != nil {
			return err
		}
		out.Version = version
		return s.audit(ctx, l, "egg.update", idString(row.ID), map[string]any{"name": name, "version": version, "source_url": source})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CustomizeEgg implements EggService.
func (s *Eggs) CustomizeEgg(ctx context.Context, req *panelv1.CustomizeEggRequest) (*panelv1.CustomizeEggResponse, error) {
	out := &panelv1.CustomizeEggResponse{}
	err := s.tx(ctx, req.GetOrgId(), true, func(l *lib) error {
		row, _, err := s.load(ctx, l, req.GetId())
		if err != nil {
			return err
		}
		file, parent, category, summary := row.Egg, row.Name, row.Category, row.Summary
		// A copy of the library's own egg is a duplicate; of anyone
		// else's, the library's customized version. Added as it is, it
		// keeps its name and can be deployed.
		suffix, status, note := " (custom)", "draft", "Copied from "+parent
		if l.owns(*row) {
			suffix = " (copy)"
		}
		if req.GetAsIs() {
			suffix, status, note = "", "published", "Added from "+parent
		}
		name := eggName(parent + suffix)
		if sp, err := spec.Read(file); err == nil && suffix != "" {
			sp.Name = name
			if edited, err := spec.Write(file, sp); err == nil {
				file = edited
			}
		}
		created, err := s.create(ctx, l, store.CreateEggParams{
			Name: name, Summary: summary, Category: category, Origin: "fork", Status: status, Egg: file,
			ParentID: idString(row.ID), ParentName: parent,
		}, note)
		if err != nil {
			return err
		}
		out.Id = idString(created.ID)
		return s.audit(ctx, l, "egg.customize", out.Id, map[string]any{"name": name, "from": req.GetId()})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetEggStatus implements EggService.
func (s *Eggs) SetEggStatus(ctx context.Context, req *panelv1.SetEggStatusRequest) (*panelv1.SetEggStatusResponse, error) {
	status := req.GetStatus()
	if status != "draft" && status != "published" && status != "archived" {
		return nil, invalid(errors.New("the status is draft, published, or archived"))
	}
	err := s.tx(ctx, req.GetOrgId(), true, func(l *lib) error {
		row, err := editable(ctx, l, req.GetId())
		if err != nil {
			return err
		}
		if row.Status == status {
			return nil
		}
		if _, err := l.q.SetEggStatus(ctx, store.SetEggStatusParams{ID: row.ID, Status: status}); err != nil {
			return err
		}
		action := map[string]string{"draft": "egg.unpublish", "published": "egg.publish", "archived": "egg.archive"}[status]
		return s.audit(ctx, l, action, idString(row.ID), map[string]any{"name": row.Name})
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.SetEggStatusResponse{}, nil
}

// DeleteEgg implements EggService.
func (s *Eggs) DeleteEgg(ctx context.Context, req *panelv1.DeleteEggRequest) (*panelv1.DeleteEggResponse, error) {
	err := s.tx(ctx, req.GetOrgId(), true, func(l *lib) error {
		row, err := editable(ctx, l, req.GetId())
		if err != nil {
			return err
		}
		counts, err := s.counts(ctx, l)
		if err != nil {
			return err
		}
		if n := servers(counts, row); n > 0 {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("%s still use this egg; archive it instead", plural(int(n), "server")))
		}
		if _, err := l.q.DeleteEgg(ctx, row.ID); err != nil {
			return err
		}
		return s.audit(ctx, l, "egg.delete", idString(row.ID), map[string]any{"name": row.Name})
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.DeleteEggResponse{}, nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ReviewEggFile implements EggService.
func (s *Eggs) ReviewEggFile(ctx context.Context, req *panelv1.ReviewEggFileRequest) (*panelv1.ReviewEggFileResponse, error) {
	var userID string
	err := s.tx(ctx, req.GetOrgId(), true, func(l *lib) error {
		userID = idString(l.userID())
		return nil
	})
	if err != nil {
		return nil, err
	}
	var src source
	switch v := req.GetSource().(type) {
	case *panelv1.ReviewEggFileRequest_Url:
		src.url = v.Url
	case *panelv1.ReviewEggFileRequest_File:
		src.file = v.File
	}
	data, from, review, err := s.Orgs.review(ctx, userID, src)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ReviewEggFileResponse{Egg: data, SourceUrl: from, Sha256: eggimport.Sum(data), Review: review}
	err = s.tx(ctx, req.GetOrgId(), true, func(l *lib) error {
		var err error
		if l.official {
			out.Review.Duplicate, err = l.q.OfficialEggExists(ctx, out.Sha256)
		} else {
			out.Review.Duplicate, err = l.q.OrgEggExists(ctx, store.OrgEggExistsParams{OrgID: l.org, Sha256: out.Sha256})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ImportEggFile implements EggService.
func (s *Eggs) ImportEggFile(ctx context.Context, req *panelv1.ImportEggFileRequest) (*panelv1.ImportEggFileResponse, error) {
	id, err := s.importFile(ctx, req.GetOrgId(), req.GetEgg(), req.GetSourceUrl(), req.GetCategory())
	if err != nil {
		return nil, err
	}
	return &panelv1.ImportEggFileResponse{Id: idString(id)}, nil
}

func (s *Eggs) importFile(ctx context.Context, orgID string, data []byte, sourceURL, category string) (pgtype.UUID, error) {
	name, err := checkFile(data)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if sourceURL != "" {
		if _, err := eggimport.ParseURL(sourceURL); err != nil {
			return pgtype.UUID{}, invalid(err)
		}
	}
	_, category, err = checkMeta("", category)
	if err != nil {
		return pgtype.UUID{}, err
	}
	var id pgtype.UUID
	err = s.tx(ctx, orgID, true, func(l *lib) error {
		sum := eggimport.Sum(data)
		var dup bool
		var err error
		if l.official {
			dup, err = l.q.OfficialEggExists(ctx, sum)
		} else {
			dup, err = l.q.OrgEggExists(ctx, store.OrgEggExistsParams{OrgID: l.org, Sha256: sum})
		}
		if err != nil {
			return err
		}
		if dup {
			return connect.NewError(connect.CodeAlreadyExists, errors.New("this egg is already in the library"))
		}
		row, err := s.create(ctx, l, store.CreateEggParams{
			Name: name, Category: category, Origin: "import", Status: "published", Egg: data, SourceUrl: sourceURL,
		}, "Imported")
		if err != nil {
			return err
		}
		id = row.ID
		return s.audit(ctx, l, "egg.import", idString(row.ID), map[string]any{
			"name": name, "source_url": sourceURL, "sha256": row.Sha256,
		})
	})
	return id, err
}
