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
	"github.com/xena-studios/raptor/internal/panel/catalog"
	"github.com/xena-studios/raptor/internal/panel/eggimport"
)

// Importing eggs (docs/PANEL.md#importing).
const (
	// MaxOrgEggs is how many eggs an org can have.
	MaxOrgEggs = 100
	// previewsPerHour bounds how often one user can make the Panel fetch
	// a URL.
	previewsPerHour = 30
	orgEggPrefix    = "org:"
	maxEggName      = 100
)

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
