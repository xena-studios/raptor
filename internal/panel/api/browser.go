package api

import (
	"net/http"
	"strings"
)

// Connect's request headers, which a cross-origin browser call needs CORS to
// allow.
var connectHeaders = strings.Join([]string{
	"Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms", "Connect-Accept-Encoding",
	"Connect-Content-Encoding", "Grpc-Timeout", "X-Grpc-Web", "X-User-Agent",
}, ", ")

// browserGuard is the API's defense against other sites
// (docs/PANEL.md#auth, DECISIONS #81 and #83): a browser request is only
// served if its Origin is the web app's, and CORS lets exactly that origin
// make credentialed calls. Every *.raptorpanel.net site is same-site, so
// SameSite cookies alone wouldn't stop the landing page or docs from
// calling the API with the user's session.
func browserGuard(appOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin != appOrigin {
			http.Error(w, "this API only answers the Raptor web app", http.StatusForbidden)
			return
		}
		if origin != "" {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", appOrigin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Expose-Headers", "Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin")
		}
		if r.Method == http.MethodOptions {
			h := w.Header()
			h.Set("Access-Control-Allow-Methods", "GET, POST")
			h.Set("Access-Control-Allow-Headers", connectHeaders)
			h.Set("Access-Control-Max-Age", "7200")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
