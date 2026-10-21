import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { type FormEvent, useState } from "react";

import { DetailList } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
import type { Org } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { dateOnly, roleNames } from "@/lib/format";
import { orgClient } from "@/lib/transport";

// OrgSettings is the org's name: admins and owners can change it.
export function OrgSettings({ org, editable }: { org: Org; editable: boolean }) {
  const client = useQueryClient();
  const [name, setName] = useState(org.name);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  async function rename(e: FormEvent) {
    e.preventDefault();
    setError("");
    setSaved(false);
    try {
      await orgClient.renameOrg({ orgId: org.id, name });
      await client.invalidateQueries();
      setSaved(true);
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Organization</CardTitle>
        <CardDescription>
          {editable ? "What your members see it called." : "Only admins and owners can change it."}
        </CardDescription>
      </CardHeader>
      <form onSubmit={rename}>
        <CardContent className="flex flex-col gap-2">
          <Label htmlFor="org-name">Name</Label>
          <Input
            id="org-name"
            required
            maxLength={64}
            readOnly={!editable}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setSaved(false);
            }}
          />
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        {editable && (
          <CardFooter className="mt-4 gap-3">
            <Button type="submit" disabled={name.trim() === org.name}>
              Save
            </Button>
            {saved && <span className="text-sm text-muted-foreground">Saved.</span>}
          </CardFooter>
        )}
      </form>
    </Card>
  );
}

export function OrgDetails({ org }: { org: Org }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Details</CardTitle>
        <CardDescription>About this org.</CardDescription>
      </CardHeader>
      <CardContent>
        <DetailList
          rows={[
            ["Org ID", <code key="id">{org.id}</code>],
            ["Created", org.createdAt ? dateOnly(timestampDate(org.createdAt)) : ""],
            ["Your role", roleNames[org.role as keyof typeof roleNames]],
          ]}
        />
      </CardContent>
    </Card>
  );
}

// LeaveOrg takes a member who isn't an owner out of the org (owners get
// DeleteOrg instead).
export function LeaveOrg({ org, userId }: { org: Org; userId: string }) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function leave() {
    setBusy(true);
    setError("");
    try {
      await orgClient.removeMember({ orgId: org.id, userId });
      try {
        localStorage.removeItem("raptor.org");
      } catch {
        // Nothing remembered.
      }
      await client.invalidateQueries();
      await navigate({ to: "/" });
    } catch (err) {
      setError(message(err));
      setBusy(false);
    }
  }

  return (
    <Card className="border-destructive/40">
      <CardHeader>
        <CardTitle>Leave organization</CardTitle>
        <CardDescription>
          You'll lose access to its nodes and servers until someone invites you back.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Button variant="destructive" onClick={() => setOpen(true)}>
          Leave organization
        </Button>
      </CardContent>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          setError("");
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Leave {org.name}?</DialogTitle>
            <DialogDescription>
              You'll lose access to its nodes and servers until someone invites you back.
            </DialogDescription>
          </DialogHeader>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button variant="destructive" disabled={busy} onClick={leave}>
              Leave
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

// DeleteOrg deletes the org (owners), after typing its name and "confirm
// it's you". Its nodes have to be removed first: that's what lets go of
// their hostnames, backup storage, and billing.
export function DeleteOrg({ org }: { org: Org }) {
  const client = useQueryClient();
  const navigate = useNavigate();
  const withReauth = useReauth();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function remove(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await withReauth(() => orgClient.deleteOrg({ orgId: org.id }));
      try {
        localStorage.removeItem("raptor.org");
      } catch {
        // Nothing remembered.
      }
      await client.invalidateQueries();
      await navigate({ to: "/" });
    } catch (err) {
      setError(message(err));
      setBusy(false);
    }
  }

  return (
    <Card className="border-destructive/40">
      <CardHeader>
        <CardTitle>Delete organization</CardTitle>
        <CardDescription>
          Takes everyone out of {org.name} and deletes it, with its invitations and join tokens.
          Remove its nodes first (each node's Settings tab): their servers keep running on the
          machines. This can't be undone.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Button variant="destructive" onClick={() => setOpen(true)}>
          Delete organization
        </Button>
      </CardContent>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          setTyped("");
          setError("");
        }}
      >
        <DialogContent>
          <form onSubmit={remove} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Delete {org.name}?</DialogTitle>
              <DialogDescription>
                Everyone is taken out of it, with its invitations and join tokens. This can't be
                undone. Type its name, <span className="font-medium">{org.name}</span>, to confirm.
              </DialogDescription>
            </DialogHeader>
            <Input
              aria-label="The organization's name"
              autoFocus
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
            />
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button
                type="submit"
                variant="destructive"
                disabled={busy || typed.trim() !== org.name}
              >
                Delete organization
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
