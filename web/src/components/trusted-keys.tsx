import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound, Trash2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { OrgService, Role, type Server } from "@/gen/raptor/panel/v1/org_pb";
import { keyFingerprint } from "@/lib/canonical";
import { message } from "@/lib/errors";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

// A key the node trusts, as Wings' keys.list reports it (byte fields in
// standard base64).
type NodeKey = {
  credential_id: string;
  fingerprint: string;
  user_id: string;
  name: string;
  role: "owner" | "delegate";
  server_id?: string;
  actions?: string[];
  expires_at?: number;
  added_by?: string;
  added_at: number;
};

const b64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));

// The signed actions a member can be allowed to approve (each also needs the
// matching server permission in the Panel).
const delegatable = [
  { action: "server.reinstall", label: "Wipe and reinstall" },
  { action: "server.update", label: "Change the egg, image, or startup" },
  { action: "backup.restore", label: "Restore backups" },
  { action: "backup.delete", label: "Delete backups" },
  { action: "backup.lock", label: "Unlock backups" },
];

// TrustedKeys manages the passkeys a node trusts: every change is signed by
// one of the owner's passkeys the node already trusts, and Wings checks it.
export function TrustedKeys({
  orgId,
  nodeId,
  userId,
  servers,
}: {
  orgId: string;
  nodeId: string;
  userId: string;
  servers: Server[];
}) {
  const client = useQueryClient();
  const keys = useQuery({
    queryKey: ["node-keys", nodeId],
    queryFn: async () => {
      const res = await commandClient.execute({ nodeId, action: "keys.list" });
      return (JSON.parse(res.resultJson || "{}") as { keys?: NodeKey[] }).keys ?? [];
    },
  });
  const members = useConnectQuery(OrgService.method.listMembers, { orgId });
  const mine = useConnectQuery(AuthService.method.listPasskeys, {});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const emails = new Map(members.data?.members.map((m) => [m.userId, m.email]) ?? []);

  async function signed(action: string, params: Record<string, unknown>) {
    setError("");
    setBusy(true);
    try {
      await sendSigned({ userId, nodeId, action, params });
      await client.invalidateQueries({ queryKey: ["node-keys", nodeId] });
      return true;
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
      return false;
    } finally {
      setBusy(false);
    }
  }

  const trusted = new Set(keys.data?.map((k) => k.credential_id));
  const addable = mine.data?.passkeys.filter((p) => !trusted.has(b64(p.credentialId))) ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRound className="size-4" /> Trusted passkeys
        </CardTitle>
        <CardDescription>
          Dangerous actions here must be signed by one of these. Changes to this list are signed by
          an owner's passkey the node already trusts.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {error && <p className="text-sm text-destructive">{error}</p>}
        {keys.error && <p className="text-sm text-destructive">{message(keys.error)}</p>}
        {keys.data?.length === 0 && (
          <p className="text-sm text-muted-foreground">None yet: pair one below.</p>
        )}
        {keys.data?.map((k) => (
          <div
            key={k.credential_id}
            className="flex items-center justify-between gap-3 rounded-lg border p-3 text-sm"
          >
            <div>
              <p className="font-medium">
                {k.name || "Passkey"}{" "}
                <Badge variant="secondary">{k.role === "owner" ? "Owner" : "Delegated"}</Badge>
              </p>
              <p className="text-xs text-muted-foreground">
                {emails.get(k.user_id) ?? k.user_id} · <code>{k.fingerprint}</code>
                {k.role === "delegate" &&
                  ` · ${k.actions?.join(", ")} on ${servers.find((s) => s.id === k.server_id)?.name ?? (k.server_id || "every server")}${
                    k.expires_at
                      ? ` until ${new Date(k.expires_at * 1000).toLocaleDateString()}`
                      : ""
                  }`}
              </p>
            </div>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`Stop trusting ${k.name}`}
              disabled={busy}
              onClick={() => signed("keys.remove", { credential_id: k.credential_id })}
            >
              <Trash2 />
            </Button>
          </div>
        ))}
        {addable.length > 0 && (
          <AddMine
            passkeys={addable}
            busy={busy}
            onAdd={(p) =>
              signed("keys.add", {
                credential_id: b64(p.credentialId),
                public_key: b64(p.publicKey),
                user_id: userId,
                role: "owner",
                name: p.name,
              })
            }
          />
        )}
        <Delegate
          orgId={orgId}
          servers={servers}
          busy={busy}
          members={members.data?.members.filter((m) => m.role === Role.MEMBER) ?? []}
          onDelegate={(params) => signed("keys.add", params)}
        />
      </CardContent>
    </Card>
  );
}

type MyPasskey = { id: string; name: string; credentialId: Uint8Array; publicKey: Uint8Array };

function AddMine({
  passkeys,
  busy,
  onAdd,
}: {
  passkeys: MyPasskey[];
  busy: boolean;
  onAdd: (p: MyPasskey) => Promise<boolean>;
}) {
  const [chosen, setChosen] = useState(passkeys[0]?.id ?? "");
  const key = passkeys.find((p) => p.id === chosen) ?? passkeys[0];
  return (
    <div className="flex items-end gap-2">
      <div className="flex flex-1 flex-col gap-1">
        <Label htmlFor="add-mine">Trust another of your passkeys as an owner</Label>
        <select
          id="add-mine"
          className="rounded-md border bg-background px-2 py-1.5 text-sm"
          value={key?.id}
          onChange={(e) => setChosen(e.target.value)}
        >
          {passkeys.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </div>
      <Button disabled={busy || !key} onClick={() => key && onAdd(key)}>
        Sign and add
      </Button>
    </div>
  );
}

function Delegate({
  orgId,
  servers,
  members,
  busy,
  onDelegate,
}: {
  orgId: string;
  servers: Server[];
  members: { userId: string; email: string }[];
  busy: boolean;
  onDelegate: (params: Record<string, unknown>) => Promise<boolean>;
}) {
  const [user, setUser] = useState("");
  const [key, setKey] = useState("");
  const [server, setServer] = useState("");
  const [actions, setActions] = useState<string[]>([]);
  const [days, setDays] = useState("30");
  const [fingerprint, setFingerprint] = useState("");
  const theirs = useConnectQuery(
    OrgService.method.listMemberPasskeys,
    { orgId, userId: user },
    { enabled: !!user },
  );
  if (members.length === 0) return null;
  const passkey = theirs.data?.passkeys.find((p) => p.id === key) ?? theirs.data?.passkeys[0];

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!passkey || actions.length === 0) return;
    const d = Number(days);
    const ok = await onDelegate({
      credential_id: b64(passkey.credentialId),
      public_key: b64(passkey.publicKey),
      user_id: user,
      role: "delegate",
      server_id: server,
      actions,
      expires_at: d > 0 ? Math.floor(Date.now() / 1000) + d * 86400 : 0,
      name: passkey.name,
    });
    if (ok) {
      setActions([]);
      setFingerprint("");
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 rounded-lg border p-3 text-sm">
      <p className="font-medium">Let a member approve dangerous actions</p>
      <div className="grid grid-cols-2 gap-2">
        <select
          className="rounded-md border bg-background px-2 py-1.5"
          value={user}
          onChange={(e) => {
            setUser(e.target.value);
            setKey("");
            setFingerprint("");
          }}
        >
          <option value="">Member…</option>
          {members.map((m) => (
            <option key={m.userId} value={m.userId}>
              {m.email}
            </option>
          ))}
        </select>
        <select
          className="rounded-md border bg-background px-2 py-1.5"
          value={passkey?.id ?? ""}
          onChange={(e) => setKey(e.target.value)}
          disabled={!theirs.data?.passkeys.length}
        >
          {user && theirs.data?.passkeys.length === 0 && <option>They have no passkey yet</option>}
          {theirs.data?.passkeys.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <select
          className="rounded-md border bg-background px-2 py-1.5"
          value={server}
          onChange={(e) => setServer(e.target.value)}
        >
          <option value="">Every server on this node</option>
          {servers.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <div className="flex items-center gap-2">
          <Input
            type="number"
            min={0}
            value={days}
            onChange={(e) => setDays(e.target.value)}
            className="w-20"
          />
          <span className="text-muted-foreground">days (0: no end)</span>
        </div>
      </div>
      <div className="flex flex-wrap gap-x-3 gap-y-1">
        {delegatable.map((a) => (
          <label key={a.action} className="flex items-center gap-1">
            <input
              type="checkbox"
              checked={actions.includes(a.action)}
              onChange={() =>
                setActions(
                  actions.includes(a.action)
                    ? actions.filter((x) => x !== a.action)
                    : [...actions, a.action],
                )
              }
            />
            {a.label}
          </label>
        ))}
      </div>
      {passkey && (
        <button
          type="button"
          className="self-start text-xs text-muted-foreground underline"
          onClick={async () => setFingerprint(await keyFingerprint(passkey.publicKey))}
        >
          {fingerprint
            ? `Their passkey's fingerprint: ${fingerprint}. Ask them to check it matches the one in their Security settings.`
            : "Show their passkey's fingerprint"}
        </button>
      )}
      <Button
        type="submit"
        className="self-start"
        disabled={busy || !passkey || actions.length === 0}
      >
        Sign and delegate
      </Button>
    </form>
  );
}
