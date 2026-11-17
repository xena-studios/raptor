package auth

import (
	"testing"

	"connectrpc.com/connect"

	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
)

// Guests may read most things and change only what the demo offers
// (#449); a new RPC is closed to them until it's listed.
func TestGuestMayCall(t *testing.T) {
	read, write := connect.IdempotencyNoSideEffects, connect.IdempotencyUnknown
	for _, c := range []struct {
		proc string
		idem connect.IdempotencyLevel
		ok   bool
	}{
		{panelv1connect.OrgServiceListServersProcedure, read, true},
		{panelv1connect.AuthServiceGetSessionProcedure, read, true},
		{panelv1connect.CommandServiceExecuteProcedure, write, true},
		{panelv1connect.OrgServiceTestConnectionProcedure, write, true},
		{panelv1connect.AuthServiceSignOutProcedure, write, true},
		{panelv1connect.OrgServiceListMembersProcedure, read, false},
		{panelv1connect.BillingServiceGetBillingProcedure, read, false},
		{panelv1connect.OrgServiceCreateOrgProcedure, write, false},
		{panelv1connect.AuthServiceBeginPasskeyRegistrationProcedure, write, false},
		{panelv1connect.AuthServiceStartEmailChangeProcedure, write, false},
		{panelv1connect.AlertServiceCreateAlertChannelProcedure, write, false},
		{panelv1connect.EggServiceShareEggProcedure, write, false},
		{panelv1connect.AuthServiceApproveAppProcedure, write, false},
		{"/raptor.panel.v1.OrgService/SomethingNew", write, false},
	} {
		if err := GuestMayCall(c.proc, c.idem); (err == nil) != c.ok {
			t.Errorf("%s: %v", c.proc, err)
		}
	}
	if DemoRestricted(false, "member") || DemoRestricted(true, "owner") || !DemoRestricted(true, "member") || !DemoRestricted(true, "admin") {
		t.Error("DemoRestricted")
	}
}
