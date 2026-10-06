import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { type FormEvent, useState } from "react";

import { AppShell } from "@/components/app-shell";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { roleNames } from "@/lib/format";
import { requireSession } from "@/lib/session";
import { orgClient } from "@/lib/transport";

export const Route = createFileRoute("/")({
  beforeLoad: ({ location }) => requireSession(location),
  component: Home,
});

function Home() {
  const session = Route.useRouteContext();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [error, setError] = useState("");

  async function create(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await orgClient.createOrg({ name });
      setName("");
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <AppShell session={session}>
      <h1 className="mb-6 font-heading text-2xl font-semibold">Orgs</h1>
      <div className="flex flex-col gap-3">
        {orgs.data?.orgs.length === 0 && (
          <p className="text-sm text-muted-foreground">
            You're not in any org yet. Make one, or accept an invitation from your email.
          </p>
        )}
        {orgs.data?.orgs.map((o) => (
          <Link key={o.id} to="/orgs/$orgId" params={{ orgId: o.id }}>
            <Card className="transition-colors hover:bg-muted/50">
              <CardHeader className="flex-row items-center justify-between">
                <CardTitle>{o.name}</CardTitle>
                <Badge variant="secondary">{roleNames[o.role]}</Badge>
              </CardHeader>
            </Card>
          </Link>
        ))}
        <Card>
          <CardContent>
            <form onSubmit={create} className="flex gap-2">
              <Input
                placeholder="New org's name"
                required
                maxLength={64}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <Button type="submit">Create org</Button>
            </form>
            {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
          </CardContent>
        </Card>
      </div>
    </AppShell>
  );
}
