package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"testing"
	"time"
)

// Standard Webhooks' own example
// (github.com/standard-webhooks/standard-webhooks/blob/main/spec/standard-webhooks.md).
func TestVerifyWebhook(t *testing.T) {
	secret := "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw" // gitleaks:allow (the spec's published example)
	body := []byte(`{"test": 2432232314}`)
	h := http.Header{}
	h.Set("webhook-id", "msg_p5jXN8AQM9LWM0D4loKWxJek")
	h.Set("webhook-timestamp", "1614265330")
	h.Set("webhook-signature", "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=")
	at := time.Unix(1614265330, 0)
	if err := VerifyWebhook(secret, h, body, at); err != nil {
		t.Fatalf("the spec's example: %v", err)
	}
	// Several signatures (a rotated secret): any one will do.
	h.Set("webhook-signature", "v1,bm9wZQ== v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE=")
	if err := VerifyWebhook(secret, h, body, at); err != nil {
		t.Errorf("among others: %v", err)
	}
	if err := VerifyWebhook(secret, h, []byte(`{"test": 1}`), at); err == nil {
		t.Error("a changed body passed")
	}
	if err := VerifyWebhook(secret, h, body, at.Add(10*time.Minute)); err == nil {
		t.Error("an old delivery passed")
	}
	if err := VerifyWebhook("whsec_b3RoZXI=", h, body, at); err == nil {
		t.Error("another secret passed")
	}
	// Secrets Polar made before 8 September 2026 key the HMAC with the
	// whole string.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("msg_p5jXN8AQM9LWM0D4loKWxJek.1614265330." + string(body)))
	h.Set("webhook-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	if err := VerifyWebhook(secret, h, body, at); err != nil {
		t.Errorf("an older secret: %v", err)
	}
}
