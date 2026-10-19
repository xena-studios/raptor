// Action toasts (docs/PANEL.md#toasts): every change made through the API
// is confirmed in a toast once the Panel or the node says it's done, by
// an interceptor on the transport, so no page has to remember to. Only the
// calls listed here toast: reads, sign-in steps, redirects (checkout), and
// calls whose result the page shows itself (a connection test, a join
// token) stay quiet. The file manager says what it did itself, with the
// file names, so files.* commands aren't here.
type Describe = string | ((req: Record<string, unknown>) => string | undefined);

const rpcs: Record<string, Describe> = {
  // Account
  "AuthService.UpdateProfile": "Profile saved",
  "AuthService.FinishEmailChange": "Email address changed",
  "AuthService.RevokeSession": "Signed that device out",
  "AuthService.FinishPasskeyRegistration": "Passkey added",
  "AuthService.RenamePasskey": "Passkey renamed",
  "AuthService.DeletePasskey": "Passkey removed",
  "AuthService.FinishTOTPSetup": "Authenticator app turned on",
  "AuthService.DisableTOTP": "Authenticator app turned off",
  "AuthService.RegenerateRecoveryCodes": "New recovery codes made",
  "AuthService.UnlinkOAuthAccount": "Account unlinked",
  // Orgs and members
  "OrgService.CreateOrg": "Organization created",
  "OrgService.RenameOrg": "Organization renamed",
  "OrgService.DeleteOrg": "Organization deleted",
  "OrgService.SetMemberRole": "Role changed",
  "OrgService.RemoveMember": "Member removed",
  "OrgService.InviteMember": "Invitation sent",
  "OrgService.RevokeInvitation": "Invitation revoked",
  "OrgService.AcceptInvitation": "Invitation accepted",
  "OrgService.SetServerAccess": "Access saved",
  // Nodes and servers
  "OrgService.RenameNode": "Node renamed",
  "OrgService.SetNodeHostname": "Hostname saved",
  "OrgService.SetServerDisplayAddress": "Shown address saved",
  "OrgService.RemoveNode": "Node removed",
  "OrgService.SetNodeSFTP": (r) =>
    r.allowed ? "SFTP allowed on this node" : "SFTP stopped on this node",
  "OrgService.CreateSFTPAccess": "SFTP turned on",
  "OrgService.RevokeSFTPAccess": "SFTP turned off",
  "OrgService.DenySupportRequest": "Support request denied",
  // Backups
  "OrgService.EnableBackupStorage": "Raptor Backup Storage turned on",
  "OrgService.DisableBackupStorage": "Raptor Backup Storage turned off",
  // Billing (a card Polar's form adds is confirmed by the page)
  "BillingService.EndTrial": "Free month ended; billing started",
  "BillingService.SetDefaultCard": "Default card changed",
  "BillingService.RemoveCard": "Card removed",
  "BillingService.CancelPlan": (r) => (r.resume ? "The plan carries on" : "Plan cancelled"),
  // Alerts
  "AlertService.CreateAlertChannel": "Alert channel added",
  "AlertService.UpdateAlertChannel": "Alert channel saved",
  "AlertService.DeleteAlertChannel": "Alert channel removed",
  "AlertService.TestAlertChannel": "Test alert sent",
  "AlertService.SaveAlertRule": "Alert rule saved",
  "AlertService.DeleteAlertRule": "Alert rule removed",
  // Eggs
  "EggService.CreateEgg": "Egg created",
  "EggService.UpdateEgg": "Egg saved",
  "EggService.CustomizeEgg": "Copy of the egg made",
  "EggService.SetEggStatus": "Egg updated",
  "EggService.DeleteEgg": "Egg deleted",
  "EggService.ImportEggFile": "Egg imported",
  "EggService.ShareEgg": (r) => (r.shared === false ? "Egg unshared" : "Egg shared"),
  "EggService.ReportEgg": "Report sent",
  "EggService.ModerateEgg": "Egg moderated",
  // Staff
  "StaffService.SuspendUser": "User suspended",
  "StaffService.UnsuspendUser": "User unsuspended",
  "StaffService.SignOutUser": "User signed out everywhere",
  "StaffService.SuspendOrg": "Organization suspended",
  "StaffService.UnsuspendOrg": "Organization unsuspended",
  "StaffService.SaveAnnouncement": "Announcement saved",
  "StaffService.EndAnnouncement": "Announcement ended",
  "StaffService.RequestSupportAccess": "Support access requested",
};

// Node commands (CommandService.Execute), by action.
const commands: Record<string, string> = {
  "server.create": "Server created; it's installing",
  "server.update": "Server settings saved",
  "server.start": "Starting the server",
  "server.stop": "Stopping the server",
  "server.restart": "Restarting the server",
  "server.kill": "Server killed",
  "server.reinstall": "Reinstalling the server",
  "server.delete": "Server deleted",
  "server.move": "Moving the server",
  "server.properties": "Server properties saved",
  "server.jar": "Server version changed",
  "schedule.create": "Schedule created",
  "schedule.update": "Schedule saved",
  "schedule.delete": "Schedule deleted",
  "schedule.run": "Schedule started",
  "backup.create": "Backing up",
  "backup.restore": "Restoring the backup",
  "backup.delete": "Backup deleted",
  "backup.lock": "Backup updated",
  "backup.extract": "Restoring to .restore",
  "backup.policy.update": "Backup plan saved",
  "backup.destination.save": "Backup destination saved",
  "backup.destination.delete": "Backup destination removed",
  "backup.key.mode": "Backup key setting saved",
  "backup.recover": "Recovering backups",
  "addons.install": "Installing",
  "node.rename": "Node renamed",
  "node.update": "Updating Raptor on the node",
  "node.reboot": "Rebooting the node",
  "node.disk.setup": "Disk set up",
  "node.disk.grow": "Disk grown",
  "node.disk.default": "Default disk changed",
  "node.firewall": "Firewall updated",
  "node.network.save": "Network saved",
  "node.network.delete": "Network deleted",
  "server.networks": "Networks changed; they apply at its next start",
  "support.grant": "Support access granted",
  "support.revoke": "Support access ended",
  "support.disable": "Support access turned off",
  "support.deny": "Support request denied",
};

// describe is the toast for a call that succeeded, if it has one.
export function describe(rpc: string, req: Record<string, unknown>): string | undefined {
  if (rpc === "CommandService.Execute") {
    return commands[String(req.action ?? "")];
  }
  const d = rpcs[rpc];
  return typeof d === "function" ? d(req) : d;
}
