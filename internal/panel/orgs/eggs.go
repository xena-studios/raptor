package orgs

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/catalog"
	"github.com/xena-studios/raptor/internal/panel/eggimport"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Imported eggs (docs/PANEL.md#imported-eggs).
const (
	// MaxOrgEggs is how many eggs an org can import.
	MaxOrgEggs = 100
	// previewsPerHour bounds how often one user can make the Panel fetch
	// a URL.
	previewsPerHour = 30
	orgEggPrefix    = "org:"
	maxEggName      = 100
)

var errNoOrgEgg = connect.NewError(connect.CodeNotFound, errors.New("that egg isn't in this org; it may have been removed"))

func (s *Service) eggClient() *http.Client {
	if s.EggClient != nil {
		return s.EggClient
	}
	return eggimport.Client()
}

func invalid(err error) error {
	return connect.NewError(connect.CodeInvalidArgument, err)
}

// source is an egg to review: a link, or a file.
type source struct {
	url  string
	file []byte
}

// review fetches or takes an egg and says what it would run. Fetching a
// link is rate limited per user.
func (s *Service) review(ctx context.Context, userID string, src source) ([]byte, string, *panelv1.EggReview, error) {
	var data []byte
	var from string
	switch {
	case src.url != "":
		u, err := eggimport.ParseURL(src.url)
		if err != nil {
			return nil, "", nil, invalid(err)
		}
		if err := s.Auth.RateLimit(ctx, "eggpreview:user:"+userID, previewsPerHour, time.Hour); err != nil {
			return nil, "", nil, err
		}
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if data, from, err = eggimport.Fetch(fctx, s.eggClient(), u); err != nil {
			return nil, "", nil, invalid(err)
		}
	case len(src.file) > 0:
		data = src.file
	default:
		return nil, "", nil, invalid(errors.New("paste a link to an egg, or choose an egg file"))
	}
	r, err := eggimport.Parse(data)
	if err != nil {
		return nil, "", nil, invalid(err)
	}
	return data, from, review(r), nil
}

// PreviewEgg implements OrgService (EggService.ReviewEggFile, for an org).
func (s *Service) PreviewEgg(ctx context.Context, req *panelv1.PreviewEggRequest) (*panelv1.PreviewEggResponse, error) {
	in := &panelv1.ReviewEggFileRequest{OrgId: req.GetOrgId()}
	switch src := req.GetSource().(type) {
	case *panelv1.PreviewEggRequest_Url:
		in.Source = &panelv1.ReviewEggFileRequest_Url{Url: src.Url}
	case *panelv1.PreviewEggRequest_File:
		in.Source = &panelv1.ReviewEggFileRequest_File{File: src.File}
	}
	r, err := (&Eggs{Orgs: s}).ReviewEggFile(ctx, in)
	if err != nil {
		return nil, err
	}
	return &panelv1.PreviewEggResponse{Egg: r.GetEgg(), SourceUrl: r.GetSourceUrl(), Sha256: r.GetSha256(), Review: r.GetReview()}, nil
}

func review(r *eggimport.Review) *panelv1.EggReview {
	e := r.Egg
	p := catalog.Proto(e)
	out := &panelv1.EggReview{
		Name: e.Name, Description: e.Description, Author: e.Author, Format: e.Format,
		Images: p.GetImages(), Registries: r.Registries, InstallContainer: e.Install.Container,
		InstallEntrypoint: e.Install.Entrypoint, InstallScript: e.Install.Script,
		Variables: p.GetVariables(), Features: e.Features, Arch: e.Raptor.Arch,
	}
	for _, c := range e.Startup {
		out.Startup = append(out.Startup, c.Command)
	}
	for _, w := range r.Warnings {
		out.Warnings = append(out.Warnings, &panelv1.EggWarning{Kind: w.Kind, Text: w.Text})
	}
	return out
}

// eggName is what an imported egg is listed as.
func eggName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Unnamed egg"
	}
	if utf8.RuneCountInString(name) > maxEggName {
		name = string([]rune(name)[:maxEggName])
	}
	return name
}

// ImportEgg implements OrgService (EggService.ImportEggFile, for an org).
func (s *Service) ImportEgg(ctx context.Context, req *panelv1.ImportEggRequest) (*panelv1.ImportEggResponse, error) {
	l := &Eggs{Orgs: s}
	id, err := l.importFile(ctx, req.GetOrgId(), req.GetEgg(), req.GetSourceUrl(), "")
	if err != nil {
		return nil, err
	}
	d, err := l.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: req.GetOrgId(), Id: idString(id)})
	if err != nil {
		return nil, err
	}
	return &panelv1.ImportEggResponse{Egg: d.GetEgg()}, nil
}

// ListOrgEggs implements OrgService: the org's own published eggs (the
// egg library's ListEggs has everything).
func (s *Service) ListOrgEggs(ctx context.Context, req *panelv1.ListOrgEggsRequest) (*panelv1.ListOrgEggsResponse, error) {
	out := &panelv1.ListOrgEggsResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "member")
		if err != nil {
			return err
		}
		rows, err := q.ListLibraryEggs(ctx, org)
		for _, r := range rows {
			if r.Egg.OrgID != org || r.Egg.Status != "published" {
				continue
			}
			p, perr := describeEgg(&lib{org: org}, r.Egg, r.CreatedByEmail.String)
			if perr != nil {
				continue
			}
			out.Eggs = append(out.Eggs, &panelv1.OrgEgg{Egg: p, ImportedByEmail: r.CreatedByEmail.String, ImportedAt: ts(r.Egg.CreatedAt)})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetOrgEgg implements OrgService (EggService.GetEggFile).
func (s *Service) GetOrgEgg(ctx context.Context, req *panelv1.GetOrgEggRequest) (*panelv1.GetOrgEggResponse, error) {
	if _, ok := eggID(req.GetEggId()); !ok {
		return nil, errNoOrgEgg
	}
	r, err := (&Eggs{Orgs: s}).GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: req.GetOrgId(), Id: req.GetEggId()})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil, errNoOrgEgg
	}
	if err != nil {
		return nil, err
	}
	return &panelv1.GetOrgEggResponse{Egg: r.GetEgg()}, nil
}

// DeleteOrgEgg implements OrgService (EggService.DeleteEgg).
func (s *Service) DeleteOrgEgg(ctx context.Context, req *panelv1.DeleteOrgEggRequest) (*panelv1.DeleteOrgEggResponse, error) {
	if _, ok := eggID(req.GetEggId()); !ok {
		return nil, errNoOrgEgg
	}
	_, err := (&Eggs{Orgs: s}).DeleteEgg(ctx, &panelv1.DeleteEggRequest{OrgId: req.GetOrgId(), Id: req.GetEggId()})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil, errNoOrgEgg
	}
	if err != nil {
		return nil, err
	}
	return &panelv1.DeleteOrgEggResponse{}, nil
}
