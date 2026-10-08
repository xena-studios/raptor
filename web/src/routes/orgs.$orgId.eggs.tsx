import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { BadgeCheck } from "lucide-react";
import { useState } from "react";

import { PageHeader } from "@/components/page";
import { Badge } from "@/components/ui/badge";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { CatalogService } from "@/gen/raptor/panel/v1/catalog_pb";
import { eggsFor } from "@/lib/servers";

export const Route = createFileRoute("/orgs/$orgId/eggs")({
  component: Eggs,
});

// Eggs is the built-in catalog: what servers can be made from.
function Eggs() {
  const { orgId } = Route.useParams();
  const catalog = useQuery(CatalogService.method.listEggs, {});
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const list = eggsFor(catalog.data?.eggs ?? [], "").filter(
    (e) => !q || e.name.toLowerCase().includes(q) || e.category.includes(q),
  );

  return (
    <>
      <PageHeader
        title="Eggs"
        description="The games and apps Raptor can run. Certified ones are tested with every release."
      />
      <Input
        type="search"
        aria-label="Search eggs"
        placeholder="Search eggs…"
        className="sm:max-w-xs"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
      />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {list.map((e) => (
          <Card key={e.id}>
            <CardHeader>
              <CardTitle className="flex items-center gap-1.5">
                {e.name}
                {e.certified && (
                  <BadgeCheck className="size-4 text-primary" aria-label="Certified" />
                )}
              </CardTitle>
              <CardDescription className="line-clamp-2">
                {e.description || e.category}
              </CardDescription>
              <div className="flex flex-wrap items-center gap-1.5 pt-1">
                <Badge variant="secondary">{e.category}</Badge>
                {e.arch.map((a) => (
                  <Badge key={a} variant="outline">
                    {a}
                  </Badge>
                ))}
                <Link
                  to="/orgs/$orgId/servers/new"
                  params={{ orgId }}
                  className="ml-auto text-sm underline-offset-4 hover:underline"
                >
                  Deploy
                </Link>
              </div>
            </CardHeader>
          </Card>
        ))}
      </div>
    </>
  );
}
