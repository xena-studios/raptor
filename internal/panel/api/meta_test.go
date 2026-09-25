package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	metav1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/meta/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/meta/v1/metav1connect"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
)

func TestGetVersion(t *testing.T) {
	srv := httptest.NewServer(Handler())
	defer srv.Close()

	for name, opt := range map[string]connect.ClientOption{
		"connect": connect.WithProtoJSON(),
		"grpcweb": connect.WithGRPCWeb(),
	} {
		t.Run(name, func(t *testing.T) {
			client := metav1connect.NewMetaServiceClient(http.DefaultClient, srv.URL+apiPrefix, opt)
			resp, err := client.GetVersion(context.Background(), &metav1.GetVersionRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetVersion() != buildinfo.Version {
				t.Fatalf("version = %q, want %q", resp.GetVersion(), buildinfo.Version)
			}
		})
	}
}
