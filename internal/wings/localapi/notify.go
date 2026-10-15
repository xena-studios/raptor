package localapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

// TestNotifications is gone: alerts are set and tested in the Panel
// (DECISIONS #269).
//
//nolint:staticcheck // deprecated in the proto, kept for older CLIs
func (s *Service) TestNotifications(context.Context, *localv1.TestNotificationsRequest) (*localv1.TestNotificationsResponse, error) {
	return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("notifications moved to the Panel: set and test alerts in your org's settings, under Alerts"))
}
