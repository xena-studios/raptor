package orgs

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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

// PreviewEgg implements OrgService.
func (s *Service) PreviewEgg(ctx context.Context, req *panelv1.PreviewEggRequest) (*panelv1.PreviewEggResponse, error) {
	var org pgtype.UUID
	var userID string
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		var err error
		org, _, err = member(ctx, q, sess, req.GetOrgId(), "admin")
		userID = idString(sess.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := &panelv1.PreviewEggResponse{}
	switch src := req.GetSource().(type) {
	case *panelv1.PreviewEggRequest_Url:
		u, err := eggimport.ParseURL(src.Url)
		if err != nil {
			return nil, invalid(err)
		}
		if err := s.Auth.RateLimit(ctx, "eggpreview:user:"+userID, previewsPerHour, time.Hour); err != nil {
			return nil, err
		}
		fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		data, from, err := eggimport.Fetch(fctx, s.eggClient(), u)
		if err != nil {
			return nil, invalid(err)
		}
		out.Egg, out.SourceUrl = data, from
	case *panelv1.PreviewEggRequest_File:
		out.Egg = src.File
	default:
		return nil, invalid(errors.New("paste a link to an egg, or choose an egg file"))
	}
	r, err := eggimport.Parse(out.Egg)
	if err != nil {
		return nil, invalid(err)
	}
	out.Sha256 = eggimport.Sum(out.Egg)
	out.Review = review(r)
	err = s.asUser(ctx, func(_ *auth.Session, q *store.Queries) error {
		dup, err := q.OrgEggExists(ctx, store.OrgEggExistsParams{OrgID: org, Sha256: out.Sha256})
		out.Review.Duplicate = dup
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
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

// orgEggProto is an imported egg as the web app lists it.
func orgEggProto(id pgtype.UUID, data []byte, source string) (*panelv1.CatalogEgg, error) {
	r, err := eggimport.Parse(data)
	if err != nil {
		return nil, err
	}
	p := catalog.Proto(r.Egg)
	p.Id, p.Category, p.Certified, p.SourceUrl = orgEggPrefix+idString(id), "imported", false, source
	p.Name = eggName(p.GetName())
	return p, nil
}

// ImportEgg implements OrgService.
func (s *Service) ImportEgg(ctx context.Context, req *panelv1.ImportEggRequest) (*panelv1.ImportEggResponse, error) {
	data := req.GetEgg()
	r, err := eggimport.Parse(data)
	if err != nil {
		return nil, invalid(err)
	}
	source := req.GetSourceUrl()
	if source != "" {
		if _, err := eggimport.ParseURL(source); err != nil {
			return nil, invalid(err)
		}
	}
	out := &panelv1.ImportEggResponse{}
	err = s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "admin")
		if err != nil {
			return err
		}
		if n, err := q.CountOrgEggs(ctx, org); err != nil {
			return err
		} else if n >= MaxOrgEggs {
			return connect.NewError(connect.CodeResourceExhausted, errors.New("an org can import up to 100 eggs; remove one you don't use first"))
		}
		sum := eggimport.Sum(data)
		row, err := q.CreateOrgEgg(ctx, store.CreateOrgEggParams{
			OrgID: org, Name: eggName(r.Egg.Name), Egg: data, Sha256: sum, SourceUrl: source, ImportedBy: sess.UserID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeAlreadyExists, errors.New("this egg is already imported"))
		}
		if err != nil {
			return err
		}
		if out.Egg, err = orgEggProto(row.ID, row.Egg, row.SourceUrl); err != nil {
			return err
		}
		return s.audit(ctx, q, sess, org, "egg.import", idString(row.ID), nil, map[string]any{
			"name": row.Name, "source_url": source, "sha256": sum,
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListOrgEggs implements OrgService. An egg that no longer parses (the
// parser got stricter) is left out rather than failing the list.
func (s *Service) ListOrgEggs(ctx context.Context, req *panelv1.ListOrgEggsRequest) (*panelv1.ListOrgEggsResponse, error) {
	out := &panelv1.ListOrgEggsResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "member")
		if err != nil {
			return err
		}
		rows, err := q.ListOrgEggs(ctx, org)
		for _, r := range rows {
			p, perr := orgEggProto(r.ID, r.Egg, r.SourceUrl)
			if perr != nil {
				continue
			}
			out.Eggs = append(out.Eggs, &panelv1.OrgEgg{Egg: p, ImportedByEmail: r.ImportedByEmail.String, ImportedAt: ts(r.ImportedAt)})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func orgEggID(id string) (pgtype.UUID, error) {
	rest, ok := strings.CutPrefix(id, orgEggPrefix)
	if !ok {
		return pgtype.UUID{}, errNoOrgEgg
	}
	u, err := parseID(rest, "egg")
	if err != nil {
		return u, errNoOrgEgg
	}
	return u, nil
}

// GetOrgEgg implements OrgService.
func (s *Service) GetOrgEgg(ctx context.Context, req *panelv1.GetOrgEggRequest) (*panelv1.GetOrgEggResponse, error) {
	id, err := orgEggID(req.GetEggId())
	if err != nil {
		return nil, err
	}
	out := &panelv1.GetOrgEggResponse{}
	err = s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "member")
		if err != nil {
			return err
		}
		row, err := q.GetOrgEgg(ctx, store.GetOrgEggParams{OrgID: org, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoOrgEgg
		}
		out.Egg = row.Egg
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteOrgEgg implements OrgService.
func (s *Service) DeleteOrgEgg(ctx context.Context, req *panelv1.DeleteOrgEggRequest) (*panelv1.DeleteOrgEggResponse, error) {
	id, err := orgEggID(req.GetEggId())
	if err != nil {
		return nil, err
	}
	err = s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "admin")
		if err != nil {
			return err
		}
		row, err := q.GetOrgEgg(ctx, store.GetOrgEggParams{OrgID: org, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoOrgEgg
		}
		if err != nil {
			return err
		}
		if _, err := q.DeleteOrgEgg(ctx, store.DeleteOrgEggParams{OrgID: org, ID: id}); err != nil {
			return err
		}
		return s.audit(ctx, q, sess, org, "egg.delete", idString(id), nil, map[string]any{"name": row.Name})
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.DeleteOrgEggResponse{}, nil
}
