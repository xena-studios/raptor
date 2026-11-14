package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/xena-studios/raptor/internal/panel/images"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// pictureHosts are where each sign-in provider serves account pictures. A
// picture is only fetched from its own provider's hosts, over https, so a
// provider's answer can't make the Panel fetch anything else.
var pictureHosts = map[string][]string{
	"google":  {"lh3.googleusercontent.com", "lh4.googleusercontent.com", "lh5.googleusercontent.com", "lh6.googleusercontent.com"},
	"github":  {"avatars.githubusercontent.com"},
	"discord": {"cdn.discordapp.com"},
}

// pictureTimeout bounds copying one picture.
const pictureTimeout = 20 * time.Second

// copyPicture copies the picture someone has on a sign-in provider to their
// account, once: only if the account never had one, not over an upload or
// after they removed theirs (users.avatar_source). It happens after the
// sign-in, in the background, so signing in never waits for it, and a
// picture that can't be fetched or isn't a picture is just skipped.
func (s *Service) copyPicture(user store.User, provider, picture string) {
	if picture == "" || user.AvatarSource != "" {
		return
	}
	s.copying.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), pictureTimeout)
		defer cancel()
		if err := s.fetchPicture(ctx, user, provider, picture); err != nil {
			s.log().Info("copying a sign-in picture failed", "provider", provider, "err", err)
		}
	})
}

// WaitPictures waits for pictures being copied (tests, shutdown).
func (s *Service) WaitPictures() { s.copying.Wait() }

func (s *Service) pictureAllowed(provider string, u *url.URL) bool {
	hosts := pictureHosts
	if s.PictureHosts != nil {
		hosts = s.PictureHosts
	}
	return u.Scheme == "https" && u.User == nil && slices.Contains(hosts[provider], u.Host)
}

func (s *Service) fetchPicture(ctx context.Context, user store.User, provider, picture string) error {
	u, err := url.Parse(picture)
	if err != nil || !s.pictureAllowed(provider, u) {
		return fmt.Errorf("%q isn't one of %s's picture hosts", picture, provider)
	}
	client := http.Client{Timeout: pictureTimeout}
	if s.Pictures != nil {
		client = *s.Pictures
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !s.pictureAllowed(provider, req.URL) {
			return errors.New("redirected away from the provider's picture hosts")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req) //nolint:gosec // only the provider's own picture hosts, checked above and on redirects
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", u.Host, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, images.MaxBytes+1))
	if err != nil {
		return err
	}
	img, err := images.Normalize(data)
	if err != nil {
		return err
	}
	q := s.q()
	if err := images.Save(ctx, q, img); err != nil {
		return err
	}
	// Nothing changes if they set or removed a picture meanwhile; the
	// unused copy is cleaned up a day later.
	_, err = q.CopyUserAvatar(ctx, store.CopyUserAvatarParams{ID: user.ID, Avatar: images.Text(img.ID), AvatarSource: provider})
	return err
}
