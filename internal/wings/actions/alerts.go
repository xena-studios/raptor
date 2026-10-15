package actions

import (
	"context"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/thresholds"
)

// NodeAlerts is node.alerts: the threshold rules this node evaluates
// (docs/WINGS.md#threshold-alerts). It isn't signed: alerts are routine,
// and a Panel that wanted to hide something could simply not send them.
const NodeAlerts = "node.alerts"

// AlertsParams are node.alerts' parameters: every rule, replacing the last.
type AlertsParams struct {
	Rules []thresholds.Rule `json:"rules"`
}

// RegisterAlerts adds node.alerts.
func RegisterAlerts(x *command.Executor, e *thresholds.Evaluator) {
	x.Register(NodeAlerts, command.Handler{Signed: command.Never, Run: func(ctx context.Context, env command.Envelope) (any, error) {
		var p AlertsParams
		if err := decode(env, &p); err != nil {
			return nil, err
		}
		if err := e.Set(ctx, p.Rules); err != nil {
			return nil, err
		}
		return map[string]int{"rules": len(p.Rules)}, nil
	}})
}
