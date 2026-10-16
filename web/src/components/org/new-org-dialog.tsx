import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Loader2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { message } from "@/lib/errors";
import { orgClient } from "@/lib/transport";

// NewOrgDialog makes an org from the org switcher, then opens it.
export function NewOrgDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function create(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const res = await orgClient.createOrg({ name: name.trim() });
      await client.invalidateQueries();
      onOpenChange(false);
      setName("");
      if (res.org) await navigate({ to: "/orgs/$orgId", params: { orgId: res.org.id } });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (busy) return;
        onOpenChange(next);
        if (!next) setError("");
      }}
    >
      <DialogContent className="sm:max-w-md">
        <form onSubmit={create} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>New org</DialogTitle>
            <DialogDescription>
              An org holds nodes and servers, and the people who manage them. You'll be its owner.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-2">
            <Label htmlFor="new-org-name">Name</Label>
            <Input
              id="new-org-name"
              autoFocus
              required
              maxLength={64}
              placeholder="My servers"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy && <Loader2 className="animate-spin" />} Create org
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
