package localapi

import (
	"context"
	"net"
	"net/http"

	"github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1/localv1connect"
)

// Dial returns a client for the local API on the socket at path.
func Dial(path string) localv1connect.LocalServiceClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
		Protocols: new(http.Protocols),
	}
	transport.Protocols.SetUnencryptedHTTP2(true)
	return localv1connect.NewLocalServiceClient(&http.Client{Transport: transport}, "http://wings")
}
