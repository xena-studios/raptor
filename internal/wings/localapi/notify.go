package localapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/notify"
)

// Notifications sends test notifications (*notify.Notifier).
type Notifications interface {
	Test(ctx context.Context) []notify.Result
}

// TestNotifications sends a test message to every target. Root only.
func (s *Service) TestNotifications(ctx context.Context, _ *localv1.TestNotificationsRequest) (*localv1.TestNotificationsResponse, error) {
	if err := requireRoot(ctx, "send test notifications"); err != nil {
		return nil, err
	}
	if s.Notify == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no notifications are set in config.yml (see docs/WINGS.md#notifications)"))
	}
	resp := &localv1.TestNotificationsResponse{}
	for _, r := range s.Notify.Test(ctx) {
		res := &localv1.NotificationResult{Target: r.Target}
		if r.Err != nil {
			res.Error = r.Err.Error()
		}
		resp.Results = append(resp.Results, res)
	}
	return resp, nil
}
