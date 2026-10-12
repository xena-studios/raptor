import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Download, Loader2 } from "lucide-react";
import { useState } from "react";

import { useReauth } from "@/components/reauth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import type { GetEggDetailsResponse } from "@/gen/raptor/panel/v1/egg_pb";
import { download, statusNames, usage } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { eggClient } from "@/lib/transport";

import type { Library } from "./library";

// EggManagement publishes, archives, exports, and deletes an egg the
// caller can edit.
export function EggManagement({ lib, d }: { lib: Library; d: GetEggDetailsResponse }) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const withReauth = useReauth();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<"archive" | "delete">();
  const egg = d.egg;
  if (!egg) return null;

  async function run(what: string, fn: () => Promise<unknown>, after?: () => void) {
    setBusy(what);
    setError("");
    try {
      await withReauth(fn);
      await client.invalidateQueries();
      setConfirm(undefined);
      after?.();
    } catch (err) {
      setError(message(err));
      setConfirm(undefined);
    } finally {
      setBusy("");
    }
  }
  const status = (s: string) =>
    run(s, () => eggClient.setEggStatus({ orgId: lib.orgId, id: egg.id, status: s }));

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between gap-2">
            Publishing
            <Badge variant={egg.status === "published" ? "default" : "secondary"}>
              {statusNames[egg.status]}
            </Badge>
          </CardTitle>
          <CardDescription>
            {lib.orgId
              ? "Published eggs can be deployed by the org's admins. Edits apply to servers made after them."
              : "Published official eggs are offered to every org."}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          {egg.status === "published" ? (
            <Button variant="outline" disabled={!!busy} onClick={() => status("draft")}>
              {busy === "draft" && <Loader2 className="animate-spin" />} Unpublish
            </Button>
          ) : (
            <Button disabled={!!busy} onClick={() => status("published")}>
              {busy === "published" && <Loader2 className="animate-spin" />}
              {egg.status === "archived" ? "Restore and publish" : "Publish"}
            </Button>
          )}
          <Button variant="outline" onClick={() => download(egg.name, d.file)}>
            <Download /> Export
          </Button>
        </CardContent>
      </Card>
      <Card className="border-destructive/30">
        <CardHeader>
          <CardTitle>Remove</CardTitle>
          <CardDescription>
            Servers made from this egg keep running on their own copy either way.{" "}
            {egg.servers === 0
              ? "No servers use it."
              : `${usage(egg.servers)} use it, so it can be archived but not deleted.`}
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          {egg.status === "archived" ? (
            <Button variant="outline" disabled={!!busy} onClick={() => status("draft")}>
              Restore as a draft
            </Button>
          ) : (
            <Button variant="outline" onClick={() => setConfirm("archive")}>
              Archive
            </Button>
          )}
          <Button
            variant="outline"
            className="text-destructive"
            disabled={egg.servers > 0}
            title={egg.servers > 0 ? "Servers use it; archive it instead" : undefined}
            onClick={() => setConfirm("delete")}
          >
            Delete
          </Button>
        </CardContent>
      </Card>
      {error && <p className="text-sm text-destructive lg:col-span-2">{error}</p>}
      <Dialog open={!!confirm} onOpenChange={(o) => !o && !busy && setConfirm(undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirm === "delete" ? `Delete ${egg.name}?` : `Archive ${egg.name}?`}
            </DialogTitle>
            <DialogDescription>
              {confirm === "delete"
                ? "It and its versions are gone for good. Export it first if you might want it back."
                : "It leaves the list and can't be deployed. You can restore it later."}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" disabled={!!busy} onClick={() => setConfirm(undefined)}>
              Cancel
            </Button>
            {confirm === "delete" ? (
              <Button
                variant="destructive"
                disabled={!!busy}
                onClick={() =>
                  run(
                    "delete",
                    () => eggClient.deleteEgg({ orgId: lib.orgId, id: egg.id }),
                    () => navigate(lib.list),
                  )
                }
              >
                {busy === "delete" && <Loader2 className="animate-spin" />} Delete
              </Button>
            ) : (
              <Button disabled={!!busy} onClick={() => status("archived")}>
                {busy === "archived" && <Loader2 className="animate-spin" />} Archive
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
