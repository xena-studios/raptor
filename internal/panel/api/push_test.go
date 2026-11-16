package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/alerts"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// pushed is one notification a fake push service got.
type pushed struct {
	Path    string
	Header  http.Header
	Payload map[string]any
}

// fakeAPNs is Apple's push service: it checks the provider token's
// signature and answers 410 for tokens starting "dead".
type fakeAPNs struct {
	t   *testing.T
	key *ecdsa.PublicKey

	mu  sync.Mutex
	got []pushed
}

func (f *fakeAPNs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.ProtoMajor != 2 {
		http.Error(w, `{"reason":"BadProtocol"}`, http.StatusBadRequest)
		return
	}
	jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "bearer ")
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		http.Error(w, `{"reason":"MissingProviderToken"}`, http.StatusForbidden)
		return
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(f.key, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		http.Error(w, `{"reason":"InvalidProviderToken"}`, http.StatusForbidden)
		return
	}
	var p map[string]any
	_ = json.NewDecoder(r.Body).Decode(&p)
	f.mu.Lock()
	f.got = append(f.got, pushed{Path: r.URL.Path, Header: r.Header.Clone(), Payload: p})
	f.mu.Unlock()
	if strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/3/device/"), "dead") {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
	}
}

func (f *fakeAPNs) last(t *testing.T) pushed {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) == 0 {
		t.Fatal("nothing was pushed to APNs")
	}
	return f.got[len(f.got)-1]
}

// fakeFCM is Google's: an OAuth token endpoint and messages:send.
type fakeFCM struct {
	mu  sync.Mutex
	got []pushed
}

func (f *fakeFCM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/token":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fcm-access","token_type":"Bearer","expires_in":3600}`))
	case "/v1/projects/raptor-test/messages:send":
		if r.Header.Get("Authorization") != "Bearer fcm-access" {
			http.Error(w, `{"error":{"status":"UNAUTHENTICATED"}}`, http.StatusUnauthorized)
			return
		}
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		f.mu.Lock()
		f.got = append(f.got, pushed{Path: r.URL.Path, Header: r.Header.Clone(), Payload: p})
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"name":"projects/raptor-test/messages/1"}`))
	default:
		http.NotFound(w, r)
	}
}

func TestPhonePush(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}

	apnsKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	apns := &fakeAPNs{t: t, key: &apnsKey.PublicKey}
	apnsSrv := httptest.NewUnstartedServer(apns)
	apnsSrv.EnableHTTP2 = true
	apnsSrv.StartTLS()
	defer apnsSrv.Close()
	fcm := &fakeFCM{}
	fcmSrv := httptest.NewTLSServer(fcm)
	defer fcmSrv.Close()
	insecure := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test servers
		ForceAttemptHTTP2: true,
	}}

	// The .p8 and the service account, as Apple and Google hand them out.
	der, _ := x509.MarshalPKCS8PrivateKey(apnsKey)
	parsed, err := alerts.ParseAPNsKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	// On one line, as the env file has it.
	if k, err := alerts.ParseAPNsKey([]byte(base64.StdEncoding.EncodeToString(der))); err != nil || !k.Equal(parsed) {
		t.Fatalf("the .p8 on one line: %v", err)
	}
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	rsaDER, _ := x509.MarshalPKCS8PrivateKey(rsaKey)
	sa, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "raptor-test", "client_email": "push@raptor-test.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER})), "private_key_id": "k1",
		"token_uri": fcmSrv.URL + "/token",
	})
	f, err := alerts.NewFCM(sa, insecure)
	if err != nil {
		t.Fatal(err)
	}
	f.Endpoint = fcmSrv.URL

	dataKey := make([]byte, 32)
	_, _ = rand.Read(dataKey)
	push := &alerts.Push{
		APNs:   &alerts.APNs{KeyID: "KEY123", TeamID: "TEAM123", Topic: "net.raptorpanel.app", Key: parsed, Production: apnsSrv.URL, Sandbox: apnsSrv.URL + "/sandbox"},
		FCM:    f,
		Client: insecure,
	}
	svc := &alerts.Service{DB: db, DataKey: dataKey, AppURL: appOrigin, AllowPrivate: true, Push: push}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, Alerts: &orgs.Alerts{Orgs: orgSvc, Alerts: svc}, AppOrigin: appOrigin}))
	defer srv.Close()

	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
		Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code},
	}}); err != nil {
		t.Fatal(err)
	}
	iphone := signInPhone(t, srv, b, "Alice's iPhone")

	// Only the app registers, with a real-looking token.
	register := func(p *phone, platform, token string, sandbox bool) (*panelv1.AlertChannel, error) {
		res, err := p.alerts.RegisterPushDevice(ctx, &panelv1.RegisterPushDeviceRequest{Platform: platform, Token: token, Sandbox: sandbox})
		return res.GetChannel(), err
	}
	if _, err := b.alerts.RegisterPushDevice(ctx, &panelv1.RegisterPushDeviceRequest{Platform: alerts.IOS, Token: strings.Repeat("a", 64)}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a browser registering for push: %v", err)
	}
	if _, err := register(iphone, alerts.IOS, "not a token!", false); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad token: %v", err)
	}
	if _, err := register(iphone, "windows", strings.Repeat("a", 64), false); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("another platform: %v", err)
	}
	ch, err := register(iphone, alerts.IOS, strings.Repeat("a", 64), true)
	if err != nil || ch.GetType() != alerts.Phone || ch.GetTarget() != "Alice's iPhone" || !ch.GetEnabled() {
		t.Fatalf("registering: %v, %v", ch, err)
	}
	// Again (a new token) keeps one channel.
	if ch2, err := register(iphone, alerts.IOS, strings.Repeat("b", 64), true); err != nil || ch2.GetId() != ch.GetId() {
		t.Fatalf("registering again: %v, %v", ch2, err)
	}
	mine, err := b.alerts.ListAlertChannels(ctx, &panelv1.ListAlertChannelsRequest{})
	if err != nil || len(mine.GetChannels()) != 2 { // email and the phone
		t.Fatalf("Alice's channels: %v, %v", mine, err)
	}
	for _, typ := range mine.GetTypes() {
		if typ == alerts.Phone {
			t.Error("phones are offered as a channel to add by hand")
		}
	}

	// The test button, from the browser: to Apple's sandbox, for the
	// sandbox token.
	if res, err := b.alerts.TestAlertChannel(ctx, &panelv1.TestAlertChannelRequest{Id: ch.GetId()}); err != nil || !res.GetOk() {
		t.Fatalf("test: %v, %v", res, err)
	}
	got := apns.last(t)
	aps, _ := got.Payload["aps"].(map[string]any)
	alert, _ := aps["alert"].(map[string]any)
	if got.Path != "/sandbox/3/device/"+strings.Repeat("b", 64) || got.Header.Get("Apns-Topic") != "net.raptorpanel.app" ||
		got.Header.Get("Apns-Push-Type") != "alert" || alert["title"] != "Test alert" || aps["interruption-level"] != "active" {
		t.Errorf("test push: %+v", got)
	}

	// A real alert through the queue: critical ones are time-sensitive.
	org, err := b.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := register(iphone, alerts.IOS, strings.Repeat("c", 64), false); err != nil {
		t.Fatal(err)
	}
	queue := func(channel string, m alerts.Message) {
		t.Helper()
		body, _ := json.Marshal(m)
		if _, err := db.Exec(ctx, "INSERT INTO alert_deliveries (org_id, channel_id, message) VALUES ($1, $2, $3)", org.GetOrg().GetId(), channel, body); err != nil {
			t.Fatal(err)
		}
		if err := svc.Deliver(ctx); err != nil {
			t.Fatal(err)
		}
	}
	queue(ch.GetId(), alerts.Message{Condition: "crash_loop", Level: alerts.Critical, Title: "Server down after repeated crashes", Server: "smp", Node: "box", ServerID: "s1", URL: appOrigin + "/x", At: time.Now()})
	got = apns.last(t)
	aps, _ = got.Payload["aps"].(map[string]any)
	alert, _ = aps["alert"].(map[string]any)
	if got.Path != "/3/device/"+strings.Repeat("c", 64) || aps["interruption-level"] != "time-sensitive" || alert["body"] != "smp on box" ||
		aps["thread-id"] != "s1" || got.Payload["url"] != appOrigin+"/x" {
		t.Errorf("crash push: %+v", got)
	}

	// Android, through FCM.
	pixel := signInPhone(t, srv, b, "Pixel 9")
	droid, err := register(pixel, alerts.Android, strings.Repeat("d", 140), true)
	if err != nil {
		t.Fatal(err)
	}
	queue(droid.GetId(), alerts.Message{Condition: "node_offline", Level: alerts.Critical, Title: "Node offline", Node: "box", NodeID: "n1", At: time.Now()})
	fcm.mu.Lock()
	if len(fcm.got) != 1 {
		t.Fatalf("FCM got %d", len(fcm.got))
	}
	msg, _ := fcm.got[0].Payload["message"].(map[string]any)
	android, _ := msg["android"].(map[string]any)
	if msg["token"] != strings.Repeat("d", 140) || android["priority"] != "HIGH" {
		t.Errorf("FCM push: %+v", fcm.got[0].Payload)
	}
	fcm.mu.Unlock()

	// Apple no longer knows the token: the phone's channel goes.
	if _, err := register(iphone, alerts.IOS, "dead"+strings.Repeat("e", 60), false); err != nil {
		t.Fatal(err)
	}
	queue(ch.GetId(), alerts.Message{Condition: "crash_loop", Level: alerts.Critical, Title: "Down", At: time.Now()})
	var n int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM alert_channels WHERE id = $1", ch.GetId()).Scan(&n)
	if n != 0 {
		t.Error("a phone Apple doesn't know is still a channel")
	}

	// Signing a phone out takes its channel with it; so does turning push
	// off in the app.
	me, _ := pixel.auth.GetSession(ctx, &panelv1.GetSessionRequest{})
	if _, err := b.auth.RevokeSession(ctx, &panelv1.RevokeSessionRequest{Target: &panelv1.RevokeSessionRequest_Id{Id: me.GetSession().GetId()}}); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(ctx, "SELECT count(*) FROM alert_channels WHERE id = $1", droid.GetId()).Scan(&n)
	if n != 0 {
		t.Error("a signed-out phone is still a channel")
	}
	again := signInPhone(t, srv, b, "Alice's iPhone")
	third, err := register(again, alerts.IOS, strings.Repeat("f", 64), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.alerts.UnregisterPushDevice(ctx, &panelv1.UnregisterPushDeviceRequest{}); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(ctx, "SELECT count(*) FROM alert_channels WHERE id = $1", third.GetId()).Scan(&n)
	if n != 0 {
		t.Error("push turned off in the app, but the channel stayed")
	}
}
