import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { Loader2 } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";

import { AppShell, lastOrg } from "@/components/app-shell";
import { PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { roleNames } from "@/lib/format";
import { requireSession } from "@/lib/session";
import { orgClient } from "@/lib/transport";

type HomeSearch = { new?: boolean };

export const Route = createFileRoute("/")({
  beforeLoad: ({ location }) => requireSession(location),
  validateSearch: (s: Record<string, unknown>): HomeSearch => ({
    new: s.new === true || s.new === "true" ? true : undefined,
  }),
  component: Home,
});

function Home() {
  const session = Route.useRouteContext();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const client = useQueryClient();
  const navigate = useNavigate();
  const making = Route.useSearch().new;
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  // Home is the org you were last in (or your first), unless you're making
  // a new one.
  useEffect(() => {
    const list = orgs.data?.orgs;
    if (making || !list?.length) return;
    const last = lastOrg();
    const to = list.find((o) => o.id === last) ?? list[0];
    if (to) void navigate({ to: "/orgs/$orgId", params: { orgId: to.id }, replace: true });
  }, [orgs.data, making, navigate]);

  async function create(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await orgClient.createOrg({ name: name.trim() });
      setName("");
      await client.invalidateQueries();
      if (res.org) await navigate({ to: "/orgs/$orgId", params: { orgId: res.org.id } });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const list = orgs.data?.orgs;
  // Someone with no org yet is new: what Raptor is, and the three steps
  // to a running server, with the first one here.
  if (list && list.length === 0 && !making) {
    return (
      <AppShell session={session}>
        <div className="mx-auto flex w-full max-w-2xl flex-col gap-6 py-4">
          <PageHeader
            eyebrow="Welcome"
            title="Let's get your first server running."
            description="Three steps, a few minutes each. Your first node is free for its first month."
          />
          <ol className="flex flex-col gap-3">
            <li>
              <Card className="ring-primary/40">
                <CardContent className="flex flex-col gap-4">
                  <Step n={1} title="Name your org">
                    An org holds your nodes, your servers, and the people who help run them. Your
                    community's or team's name works well; you can change it later.
                  </Step>
                  <form onSubmit={create} className="flex flex-col gap-2 sm:flex-row">
                    <Label htmlFor="org-name" className="sr-only">
                      Org name
                    </Label>
                    <Input
                      id="org-name"
                      autoFocus
                      placeholder="Blocktopia, Friday Night Rust…"
                      required
                      maxLength={64}
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                    />
                    <Button type="submit" disabled={busy || !name.trim()}>
                      {busy && <Loader2 className="animate-spin" />} Create org
                    </Button>
                  </form>
                  {error && <p className="text-sm text-destructive">{error}</p>}
                </CardContent>
              </Card>
            </li>
            <li>
              <Card className="opacity-70">
                <CardContent>
                  <Step n={2} title="Connect a node">
                    Run one command on a Linux machine you own or rent: a dedicated server, a VPS,
                    or a box at home. It installs everything and links the machine.
                  </Step>
                </CardContent>
              </Card>
            </li>
            <li>
              <Card className="opacity-70">
                <CardContent>
                  <Step n={3} title="Deploy a server">
                    Pick a game and Raptor installs it and starts it. Players connect straight to
                    your machine.
                  </Step>
                </CardContent>
              </Card>
            </li>
          </ol>
          <p className="text-sm text-muted-foreground">
            Invited to someone else's org? Open the link in the invitation email to join it instead.
            New to Raptor?{" "}
            <a
              href="https://docs.raptorpanel.net/start/how-it-works/"
              target="_blank"
              rel="noreferrer"
              className="text-foreground underline underline-offset-2"
            >
              How it works
            </a>
            .
          </p>
        </div>
      </AppShell>
    );
  }

  return (
    <AppShell session={session}>
      <h1 className="mb-6 font-heading text-2xl font-semibold">{making ? "New org" : "Orgs"}</h1>
      <div className="flex flex-col gap-3">
        {list?.map((o) => (
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
            <form onSubmit={create} className="flex flex-col gap-2 sm:flex-row">
              <Label htmlFor="org-name" className="sr-only">
                Org name
              </Label>
              <Input
                id="org-name"
                placeholder="New org's name"
                required
                maxLength={64}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <Button type="submit" disabled={busy || !name.trim()}>
                {busy && <Loader2 className="animate-spin" />} Create org
              </Button>
            </form>
            {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
          </CardContent>
        </Card>
      </div>
    </AppShell>
  );
}

// Step is one of the first-run steps: its number, as the app numbers
// sequences, and what it means.
function Step({ n, title, children }: { n: number; title: string; children: ReactNode }) {
  return (
    <div className="flex gap-4">
      <span className="font-mono text-sm text-primary tabular-nums">
        {String(n).padStart(2, "0")}
      </span>
      <div className="space-y-1">
        <h2 className="font-medium">{title}</h2>
        <p className="text-sm text-muted-foreground">{children}</p>
      </div>
    </div>
  );
}
