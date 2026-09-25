package api

import (
	"context"

	metav1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/meta/v1"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
)

type metaService struct{}

func (metaService) GetVersion(context.Context, *metav1.GetVersionRequest) (*metav1.GetVersionResponse, error) {
	return &metav1.GetVersionResponse{
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Date:    buildinfo.Date,
	}, nil
}
