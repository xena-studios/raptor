import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";

import { Button } from "@/components/ui/button";
import { MetaService } from "@/gen/raptor/meta/v1/meta_pb";

export const Route = createFileRoute("/")({
  component: Index,
});

function Index() {
  const { data, error, isPending, refetch, isFetching } = useQuery(MetaService.method.getVersion, {});

  return (
    <main className="mx-auto max-w-xl p-8">
      <h1 className="font-heading text-2xl font-semibold">Raptor</h1>
      <p className="mt-2 text-sm text-muted-foreground">
        {isPending && "Connecting to the Panel…"}
        {error && `Panel unreachable: ${error.message}`}
        {data && `Panel ${data.version} (${data.commit})`}
      </p>
      <Button className="mt-4" variant="outline" disabled={isFetching} onClick={() => refetch()}>
        Refresh
      </Button>
    </main>
  );
}
