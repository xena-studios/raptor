package alerts

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

// Push notifications to Raptor's phone app (docs/PANEL.md#alerts): a
// person's phone channel holds the push token the app registered, and the
// Panel sends straight to Apple (APNs) and Google (FCM), with no push
// service in between. Each is off until its keys are set.

// Phone platforms.
const (
	IOS     = "ios"
	Android = "android"
)

var pushToken = regexp.MustCompile(`^[A-Za-z0-9_:.\-]+$`)

// gone is a push token Apple or Google no longer knows (the app was
// removed, or the token replaced): the phone's channel is deleted, not
// retried or turned off.
type gone struct{ error }

func (g gone) Unwrap() error { return g.error }

// Push sends to phones.
type Push struct {
	APNs *APNs
	FCM  *FCM
	// Client must speak HTTP/2 (APNs requires it); nil: a default one.
	Client *http.Client
}

// Offers reports whether pushes to the platform can be sent.
func (p *Push) Offers(platform string) bool {
	switch {
	case p == nil:
		return false
	case platform == IOS:
		return p.APNs != nil
	case platform == Android:
		return p.FCM != nil
	}
	return false
}

func (p *Push) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// pushMessage is an alert as a notification.
type pushMessage struct {
	Title, Body string
	Urgent      bool
	// Group is what notifications are stacked by (a server, a node).
	Group string
	Data  map[string]string
}

func pushFor(m Message) pushMessage {
	body := where(m)
	if m.Text != "" {
		if body != "" {
			body += "\n"
		}
		body += m.Text
	}
	p := pushMessage{
		Title: truncate(m.Title, 120), Body: truncate(body, 400), Urgent: m.Level == Critical && !m.Resolved,
		Group: cmpOr(m.ServerID, m.NodeID, m.Condition),
		Data:  map[string]string{},
	}
	for k, v := range map[string]string{"url": m.URL, "org_id": m.OrgID, "node_id": m.NodeID, "server_id": m.ServerID, "condition": m.Condition, "level": m.Level} {
		if v != "" {
			p.Data[k] = v
		}
	}
	return p
}

func cmpOr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (p *Push) send(ctx context.Context, platform, token string, sandbox bool, m pushMessage) error {
	switch platform {
	case IOS:
		if p == nil || p.APNs == nil {
			return errors.New("this Panel can't send to iPhones yet")
		}
		return p.APNs.send(ctx, p.client(), token, sandbox, m)
	case Android:
		if p == nil || p.FCM == nil {
			return errors.New("this Panel can't send to Android phones yet")
		}
		return p.FCM.send(ctx, p.client(), token, m)
	}
	return permanent{fmt.Errorf("unknown phone platform %q", platform)}
}

// APNs is Apple's push service, with token-based auth (a .p8 key).
type APNs struct {
	KeyID, TeamID string
	// Topic is the app's bundle ID.
	Topic string
	Key   *ecdsa.PrivateKey
	// Production and Sandbox are the servers (tests point them elsewhere);
	// empty: Apple's. Development builds of the app get sandbox tokens.
	Production, Sandbox string
	Now                 func() time.Time

	mu     sync.Mutex
	jwt    string
	madeAt time.Time
}

// ParseAPNsKey reads the .p8 key Apple gives (PKCS #8, P-256): the file's
// contents, or the base64 between its BEGIN and END lines on one line (env
// files hold one line).
func ParseAPNsKey(p8 []byte) (*ecdsa.PrivateKey, error) {
	var der []byte
	if b, _ := pem.Decode(p8); b != nil {
		der = b.Bytes
	} else {
		d, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(p8)), ""))
		if err != nil {
			return nil, errors.New("the APNs key isn't the .p8 file's contents")
		}
		der = d
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("the APNs key isn't an EC key")
	}
	return ec, nil
}

func (a *APNs) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// token is the provider token: an ES256 JWT, which Apple wants renewed
// between 20 and 60 minutes old.
func (a *APNs) token(fresh bool) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if !fresh && a.jwt != "" && now.Sub(a.madeAt) < 45*time.Minute {
		return a.jwt, nil
	}
	enc := base64.RawURLEncoding
	head, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": a.KeyID})
	claims, _ := json.Marshal(map[string]any{"iss": a.TeamID, "iat": now.Unix()})
	signing := enc.EncodeToString(head) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, a.Key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	a.jwt, a.madeAt = signing+"."+enc.EncodeToString(sig), now
	return a.jwt, nil
}

func (a *APNs) send(ctx context.Context, c *http.Client, device string, sandbox bool, m pushMessage) error {
	host := cmpOr(a.Production, "https://api.push.apple.com")
	if sandbox {
		host = cmpOr(a.Sandbox, "https://api.sandbox.push.apple.com")
	}
	level := "active"
	if m.Urgent {
		level = "time-sensitive"
	}
	payload := map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{"title": m.Title, "body": m.Body}, "sound": "default",
			"interruption-level": level, "thread-id": m.Group,
		},
	}
	for k, v := range m.Data {
		payload[k] = v
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return permanent{err}
	}
	for try := range 2 {
		tok, err := a.token(try > 0)
		if err != nil {
			return permanent{err}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/3/device/"+url.PathEscape(device), bytes.NewReader(body))
		if err != nil {
			return permanent{err}
		}
		req.Header.Set("Authorization", "bearer "+tok)
		req.Header.Set("Apns-Topic", a.Topic)
		req.Header.Set("Apns-Push-Type", "alert")
		req.Header.Set("Apns-Priority", "10")
		req.Header.Set("Apns-Expiration", fmt.Sprint(a.now().Add(24*time.Hour).Unix()))
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			return err
		}
		var out struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out)
		_ = res.Body.Close()
		switch {
		case res.StatusCode == http.StatusOK:
			return nil
		case res.StatusCode == http.StatusGone,
			out.Reason == "BadDeviceToken", out.Reason == "DeviceTokenNotForTopic", out.Reason == "Unregistered":
			return permanent{gone{fmt.Errorf("APNs: %s", cmpOr(out.Reason, "Unregistered"))}}
		case res.StatusCode == http.StatusForbidden && out.Reason == "ExpiredProviderToken" && try == 0:
			continue // a new provider token, once
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
			return fmt.Errorf("APNs: HTTP %d %s", res.StatusCode, out.Reason)
		default:
			return permanent{fmt.Errorf("APNs: HTTP %d %s", res.StatusCode, out.Reason)}
		}
	}
	return errors.New("APNs refused the provider token")
}

// FCM is Firebase Cloud Messaging (HTTP v1), with a service account.
type FCM struct {
	ProjectID string
	Tokens    oauth2.TokenSource
	// Endpoint is FCM's (tests point it elsewhere); empty: Google's.
	Endpoint string
}

// NewFCM reads a service account's JSON key (Firebase console, Project
// settings, Service accounts, Generate new private key). client fetches its
// access tokens (nil: the default).
func NewFCM(credentials []byte, client *http.Client) (*FCM, error) {
	var sa struct {
		Type        string `json:"type"`
		ProjectID   string `json:"project_id"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		KeyID       string `json:"private_key_id"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(credentials, &sa); err != nil {
		return nil, fmt.Errorf("the FCM credentials aren't a service account's JSON key: %w", err)
	}
	if sa.Type != "service_account" || sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("the FCM credentials aren't a service account's JSON key")
	}
	cfg := &jwt.Config{
		Email: sa.ClientEmail, PrivateKey: []byte(sa.PrivateKey), PrivateKeyID: sa.KeyID,
		TokenURL: cmpOr(sa.TokenURI, "https://oauth2.googleapis.com/token"),
		Scopes:   []string{"https://www.googleapis.com/auth/firebase.messaging"},
	}
	ctx := context.Background()
	if client != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	}
	return &FCM{ProjectID: sa.ProjectID, Tokens: oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx))}, nil
}

func (f *FCM) send(ctx context.Context, c *http.Client, device string, m pushMessage) error {
	tok, err := f.Tokens.Token()
	if err != nil {
		return fmt.Errorf("FCM sign-in: %w", err)
	}
	priority, channel := "NORMAL", "alerts"
	if m.Urgent {
		priority, channel = "HIGH", "urgent"
	}
	msg := map[string]any{
		"token":        device,
		"notification": map[string]string{"title": m.Title, "body": m.Body},
		"data":         m.Data,
		"android": map[string]any{
			"priority": priority, "ttl": "86400s",
			"notification": map[string]any{"channel_id": channel, "tag": m.Group},
		},
	}
	body, err := json.Marshal(map[string]any{"message": msg})
	if err != nil {
		return permanent{err}
	}
	endpoint := strings.TrimSuffix(cmpOr(f.Endpoint, "https://fcm.googleapis.com"), "/") + "/v1/projects/" + url.PathEscape(f.ProjectID) + "/messages:send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return permanent{err}
	}
	tok.SetAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var out struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	code := out.Error.Status
	for _, d := range out.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
		}
	}
	switch {
	case res.StatusCode == http.StatusOK:
		return nil
	case res.StatusCode == http.StatusNotFound || code == "UNREGISTERED":
		return permanent{gone{errors.New("FCM: UNREGISTERED")}}
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 || res.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("FCM: HTTP %d %s", res.StatusCode, code)
	default:
		return permanent{fmt.Errorf("FCM: HTTP %d %s %s", res.StatusCode, code, truncate(out.Error.Message, 200))}
	}
}
