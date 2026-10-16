import { useQuery } from "@connectrpc/connect-query";
import { cn } from "cn";

import { MetaService } from "@/gen/raptor/meta/v1/meta_pb";

const repo = "https://github.com/xena-studios/raptor";

// SourceLink offers the running Panel's source, as the AGPL asks of a
// network service: the commit the API was built from, or the repository
// for development builds.
export function SourceLink({ className }: { className?: string }) {
  const v = useQuery(MetaService.method.getVersion, {}, { staleTime: Number.POSITIVE_INFINITY });
  const commit = v.data?.commit;
  const href = commit && /^[0-9a-f]{7,40}$/.test(commit) ? `${repo}/tree/${commit}` : repo;
  return (
    <p className={cn("text-xs text-muted-foreground", className)}>
      Raptor is free software under the AGPL.{" "}
      <a href={href} target="_blank" rel="noreferrer" className="underline hover:text-foreground">
        Source code
      </a>
      {v.data?.version && v.data.version !== "dev" ? ` (${v.data.version})` : ""}
    </p>
  );
}
