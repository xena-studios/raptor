// Package catalog serves CatalogService: the built-in egg catalog, for
// creating servers in the web app (docs/EGGS.md#built-in-catalog).
package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"connectrpc.com/connect"

	catalog "github.com/xena-studios/raptor/eggs"
	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
)

// Service is the CatalogService handler.
type Service struct {
	Auth *auth.Service
}

var _ panelv1connect.CatalogServiceHandler = (*Service)(nil)

var errNoEgg = connect.NewError(connect.CodeNotFound, errors.New("no such egg in the catalog"))

// The catalog is built into the binary, so it's loaded once.
var load = sync.OnceValues(func() (*loaded, error) {
	all, err := catalog.All()
	if err != nil {
		return nil, err
	}
	l := &loaded{eggs: map[string][]byte{}}
	for _, e := range all {
		egg, err := eggs.Parse(e.Egg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.ID, err)
		}
		file, err := e.ServerEgg()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.ID, err)
		}
		l.eggs[e.ID] = file
		l.list = append(l.list, describe(e, egg))
	}
	return l, nil
})

type loaded struct {
	list []*panelv1.CatalogEgg
	eggs map[string][]byte
}

func describe(e catalog.Entry, egg *eggs.Egg) *panelv1.CatalogEgg {
	category, _, _ := strings.Cut(e.ID, "/")
	out := Proto(egg)
	out.Id, out.Category, out.Certified, out.Arch = e.ID, category, e.Certified, e.Arch
	out.SourceUrl = fmt.Sprintf("https://github.com/%s/blob/%s/%s", e.Source.Repo, e.Source.Commit, e.Source.Path)
	return out
}

// Proto is what the web app needs of an egg to create a server from it:
// the caller fills in its ID, category, and source.
func Proto(egg *eggs.Egg) *panelv1.CatalogEgg {
	out := &panelv1.CatalogEgg{Name: egg.Name, Description: egg.Description, Features: egg.Features, Arch: egg.Raptor.Arch}
	for _, i := range egg.Images {
		out.Images = append(out.Images, &panelv1.EggImage{Name: i.Name, Ref: i.Ref})
	}
	for _, v := range egg.Variables {
		out.Variables = append(out.Variables, &panelv1.EggVariable{
			Name: v.Name, Description: v.Description, Env: v.Env, Default: v.Default,
			UserViewable: v.UserViewable, UserEditable: v.UserEditable, Rules: v.Rules,
		})
	}
	return out
}

// ListEggs implements CatalogService.
func (s *Service) ListEggs(ctx context.Context, _ *panelv1.ListEggsRequest) (*panelv1.ListEggsResponse, error) {
	if _, err := s.Auth.Current(ctx); err != nil {
		return nil, err
	}
	l, err := load()
	if err != nil {
		return nil, err
	}
	return &panelv1.ListEggsResponse{Eggs: l.list}, nil
}

// GetEgg implements CatalogService.
func (s *Service) GetEgg(ctx context.Context, req *panelv1.GetEggRequest) (*panelv1.GetEggResponse, error) {
	if _, err := s.Auth.Current(ctx); err != nil {
		return nil, err
	}
	l, err := load()
	if err != nil {
		return nil, err
	}
	egg, ok := l.eggs[req.GetId()]
	if !ok {
		return nil, errNoEgg
	}
	return &panelv1.GetEggResponse{Egg: egg}, nil
}
