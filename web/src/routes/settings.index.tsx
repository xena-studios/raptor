import { createFileRoute } from "@tanstack/react-router";

import { Appearance, DeleteAccount, Details, Profile } from "@/components/account/general";
import { LinkedAccounts, linkErrors, providerNames } from "@/components/account/security";
import { Alert, AlertDescription } from "@/components/ui/alert";

type Search = { linked?: string; error?: string };

export const Route = createFileRoute("/settings/")({
  // Linking an account comes back here with how it went.
  validateSearch: (s: Record<string, unknown>): Search => ({
    linked: typeof s.linked === "string" ? s.linked : undefined,
    error: typeof s.error === "string" ? s.error : undefined,
  }),
  component: General,
});

function General() {
  const { user } = Route.useRouteContext();
  const { linked, error } = Route.useSearch();
  if (!user) return null;
  return (
    <div className="max-w-2xl space-y-6">
      {linked && (
        <Alert>
          <AlertDescription>
            {providerNames[linked] ?? linked} can now sign in to your account.
          </AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{linkErrors[error] ?? linkErrors.oauth_failed}</AlertDescription>
        </Alert>
      )}
      <Profile key={`${user.name}/${user.email}`} user={user} />
      <Appearance />
      <LinkedAccounts />
      <Details user={user} />
      <DeleteAccount email={user.email} />
    </div>
  );
}
