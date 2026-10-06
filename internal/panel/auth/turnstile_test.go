package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTurnstile(t *testing.T) {
	var got map[string]string
	answer := map[string]any{"success": true, "hostname": "verify.example.test"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = map[string]string{"secret": r.PostFormValue("secret"), "response": r.PostFormValue("response"), "remoteip": r.PostFormValue("remoteip")}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	defer srv.Close()
	ctx := context.Background()
	ts := Turnstile{Secret: "s3cret", Hostname: "verify.example.test", URL: srv.URL}

	if err := ts.Verify(ctx, "tok", "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if got["secret"] != "s3cret" || got["response"] != "tok" || got["remoteip"] != "203.0.113.7" {
		t.Errorf("sent %v", got)
	}
	if err := ts.Verify(ctx, "", ""); err == nil {
		t.Error("an empty token passed")
	}
	// Solved on some other page with our site key.
	answer["hostname"] = "evil.example.test"
	if err := ts.Verify(ctx, "tok", ""); err == nil {
		t.Error("a token from another hostname passed")
	}
	answer = map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}
	if err := ts.Verify(ctx, "tok", ""); err == nil {
		t.Error("a failed check passed")
	}
}
