import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { FileCode, Loader2, ShieldAlert, ShieldCheck, Trash2 } from "lucide-react";
import { useState } from "react";

import { useConfirm } from "@/components/confirm";
import { useFilesApi } from "@/components/file-manager/data";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { message } from "@/lib/errors";
import {
  groupFindings,
  type ScanReport,
  type Severity,
  scanSummary,
  splitPath,
  stoppedText,
} from "@/lib/fivem";

// scanKey is the last scan's query, which Needs attention reads too.
export const scanKey = (nodeId: string, serverId: string) => ["files.scan", nodeId, serverId];

// useLastScan is this session's last scan of a server, if one ran.
export function useLastScan(nodeId: string, serverId: string) {
  const api = useFilesApi(nodeId, serverId);
  return useTanQuery({
    queryKey: scanKey(nodeId, serverId),
    queryFn: () => api.run<ScanReport>("files.scan", {}),
    enabled: false,
    staleTime: Number.POSITIVE_INFINITY,
    retry: false,
  });
}

const badge: Record<Severity, "destructive" | "default" | "secondary"> = {
  high: "destructive",
  medium: "default",
  low: "secondary",
};

// ResourceScan checks a FiveM server's resources for known backdoors
// (#428): Wings reads them on the node (files.scan) and lists suspicious
// lines with how sure it is. Nothing is changed; each file can be opened,
// or moved to the trash once confirmed.
export function ResourceScan({
  orgId,
  nodeId,
  serverId,
  canWrite,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  canWrite: boolean;
}) {
  const scan = useLastScan(nodeId, serverId);
  const [open, setOpen] = useState(false);
  const r = scan.data;
  const found = r ? r.findings.length : 0;
  return (
    <Card id="resource-scan" className="scroll-mt-20">
      <CardHeader>
        <CardTitle>Resource scan</CardTitle>
        <CardDescription>
          Checks your resources for known backdoors: hidden code in leaked or bought resources that
          gives strangers control of the server. Nothing is changed.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col items-start gap-3 text-sm">
        {r && (
          <p className="flex items-start gap-2">
            {found ? (
              <ShieldAlert className="mt-0.5 size-4 shrink-0 text-destructive" />
            ) : (
              <ShieldCheck className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            )}
            <span>
              {scanSummary(r)} {stoppedText(r)}
            </span>
          </p>
        )}
        {scan.error && <p className="text-destructive">{message(scan.error)}</p>}
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={scan.isFetching}
            onClick={() => scan.refetch()}
          >
            {scan.isFetching && <Loader2 className="animate-spin" />}
            {r ? "Scan again" : "Scan resources"}
          </Button>
          {found > 0 && (
            <Button size="sm" onClick={() => setOpen(true)}>
              Show what it found
            </Button>
          )}
        </div>
      </CardContent>
      {r && (
        <ScanResults
          open={open}
          onOpenChange={setOpen}
          report={r}
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          canWrite={canWrite}
        />
      )}
    </Card>
  );
}

function ScanResults({
  open,
  onOpenChange,
  report,
  orgId,
  nodeId,
  serverId,
  canWrite,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  report: ScanReport;
  orgId: string;
  nodeId: string;
  serverId: string;
  canWrite: boolean;
}) {
  const api = useFilesApi(nodeId, serverId);
  const client = useQueryClient();
  const { confirm } = useConfirm();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const groups = groupFindings(report.findings);

  async function trash(file: string) {
    const ok = await confirm({
      title: `Move ${splitPath(file).name} to the trash?`,
      description:
        "The resource may stop working without it. You can restore it from the trash in Files. Restart the server so it stops running.",
      confirm: "Move to trash",
      destructive: true,
    });
    if (!ok) return;
    setBusy(file);
    setError("");
    try {
      const { dir, name } = splitPath(file);
      await api.run("files.delete", { dir, names: [name] });
      await api.refresh();
      // Drop it from the results, as a new scan would.
      client.setQueryData<ScanReport>(
        scanKey(nodeId, serverId),
        (old) => old && { ...old, findings: old.findings.filter((f) => f.file !== file) },
      );
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>What the scan found</DialogTitle>
          <DialogDescription>
            High means honest resources almost never do this; low is common in honest code too. Look
            at each line before removing anything: Raptor never deletes a file itself.
          </DialogDescription>
        </DialogHeader>
        {error && <p className="text-sm text-destructive">{error}</p>}
        {groups.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing left to look at.</p>
        ) : (
          <ul className="-mx-1 flex max-h-[60vh] flex-col gap-3 overflow-y-auto px-1">
            {groups.map((g) => {
              const { dir } = splitPath(g.file);
              return (
                <li key={g.file} className="flex flex-col gap-2 rounded-lg border p-3 text-sm">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant={badge[g.severity]}>{g.severity}</Badge>
                    <code className="min-w-0 flex-1 basis-48 break-all text-xs">{g.file}</code>
                    <Link
                      to="/orgs/$orgId/servers/$serverId/files"
                      params={{ orgId, serverId }}
                      search={{ path: dir || undefined, file: g.file }}
                      className={buttonVariants({ size: "sm", variant: "outline" })}
                    >
                      <FileCode /> Open
                    </Link>
                    {canWrite && (
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy === g.file}
                        onClick={() => trash(g.file)}
                      >
                        {busy === g.file ? <Loader2 className="animate-spin" /> : <Trash2 />}
                        Move to trash
                      </Button>
                    )}
                  </div>
                  <ul className="flex flex-col gap-2">
                    {g.findings.map((f) => (
                      <li key={`${f.line}:${f.rule}`} className="flex flex-col gap-1">
                        <span>
                          <span className="text-muted-foreground">Line {f.line}:</span> {f.title}{" "}
                          {g.findings.some((x) => x.severity !== g.severity) && (
                            <span className="text-xs text-muted-foreground">({f.severity})</span>
                          )}
                        </span>
                        <code className="block break-all rounded bg-muted px-2 py-1 text-xs">
                          {f.excerpt}
                        </code>
                      </li>
                    ))}
                  </ul>
                </li>
              );
            })}
          </ul>
        )}
      </DialogContent>
    </Dialog>
  );
}
