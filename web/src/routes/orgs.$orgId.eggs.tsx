import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { BadgeCheck, Download, Trash2 } from "lucide-react";
import { type ReactNode, useState } from "react";

import { EggImport } from "@/components/egg-import";
import { PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { type CatalogEgg, CatalogService } from "@/gen/raptor/panel/v1/catalog_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { isAdmin } from "@/lib/format";
import { eggsFor } from "@/lib/servers";
import { orgClient } from "@/lib/transport";

export const Route = createFileRoute("/orgs/$orgId/eggs")({
  component: Eggs,
});

// Eggs is what servers can be made from: the org's imported eggs, then the
// built-in catalog.
function Eggs() {
  const { orgId } = Route.useParams();
  const client = useQueryClient();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const admin = isAdmin(orgs.data?.orgs.find((o) => o.id === orgId)?.role);
  const catalog = useQuery(CatalogService.method.listEggs, {});
  const imported = useQuery(OrgService.method.listOrgEggs, { orgId });
  const [query, setQuery] = useState("");
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState("");
  const q = query.trim().toLowerCase();
  const matches = (e: CatalogEgg) =>
    !q || e.name.toLowerCase().includes(q) || e.category.includes(q);
  const list = eggsFor(catalog.data?.eggs ?? [], "").filter(matches);
  const own = (imported.data?.eggs ?? []).filter((o) => o.egg && matches(o.egg));

  async function remove(id: string, name: string) {
    if (
      !window.confirm(`Remove "${name}"? Servers made from it keep running; new ones can't use it.`)
    )
      return;
    setError("");
    try {
      await orgClient.deleteOrgEgg({ orgId, eggId: id });
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <>
      <PageHeader
        title="Eggs"
        description="The games and apps Raptor can run. Certified ones are tested with every release."
        actions={
          admin && (
            <Button onClick={() => setImporting(true)}>
              <Download /> Import egg
            </Button>
          )
        }
      />
      <Input
        type="search"
        aria-label="Search eggs"
        placeholder="Search eggs…"
        className="sm:max-w-xs"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />
      {error && <p className="text-sm text-destructive">{error}</p>}
      {own.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="text-sm font-medium">Imported by your org</h2>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {own.map(({ egg, importedByEmail }) =>
              egg ? (
                <EggCard
                  key={egg.id}
                  orgId={orgId}
                  egg={egg}
                  note={importedByEmail ? `Imported by ${importedByEmail}` : "Imported"}
                  action={
                    admin && (
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={`Remove ${egg.name}`}
                        className="text-muted-foreground hover:text-destructive"
                        onClick={() => remove(egg.id, egg.name)}
                      >
                        <Trash2 />
                      </Button>
                    )
                  }
                />
              ) : null,
            )}
          </div>
        </section>
      )}
      <section className="flex flex-col gap-3">
        {own.length > 0 && <h2 className="text-sm font-medium">Raptor's catalog</h2>}
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((e) => (
            <EggCard key={e.id} orgId={orgId} egg={e} />
          ))}
        </div>
      </section>
      {admin && <EggImport orgId={orgId} open={importing} onOpenChange={setImporting} />}
    </>
  );
}

function EggCard({
  orgId,
  egg,
  note,
  action,
}: {
  orgId: string;
  egg: CatalogEgg;
  note?: string;
  action?: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-1.5">
          <span className="truncate">{egg.name}</span>
          {egg.certified && (
            <BadgeCheck className="size-4 shrink-0 text-primary" aria-label="Certified" />
          )}
        </CardTitle>
        <CardDescription className="line-clamp-2">
          {egg.description || note || egg.category}
        </CardDescription>
        <div className="flex flex-wrap items-center gap-1.5 pt-1">
          <Badge variant="secondary">{egg.category}</Badge>
          {egg.arch.map((a) => (
            <Badge key={a} variant="outline">
              {a}
            </Badge>
          ))}
          {note && egg.description && (
            <span className="truncate text-xs text-muted-foreground">{note}</span>
          )}
          <span className="ml-auto flex items-center gap-1">
            {action}
            <Link
              to="/orgs/$orgId/servers/new"
              params={{ orgId }}
              className="text-sm underline-offset-4 hover:underline"
            >
              Deploy
            </Link>
          </span>
        </div>
      </CardHeader>
    </Card>
  );
}
