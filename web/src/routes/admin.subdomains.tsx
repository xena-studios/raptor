import { createFileRoute } from "@tanstack/react-router";
import { Loader2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { useReauth } from "@/components/reauth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { message } from "@/lib/errors";
import { staffClient } from "@/lib/transport";

export const Route = createFileRoute("/admin/subdomains")({
  component: SubdomainsPage,
});

// Player subdomains reported for abuse (docs/PANEL.md#subdomains): banning
// one removes its records and keeps the name from being claimed again.
function SubdomainsPage() {
  const withReauth = useReauth();
  const [name, setName] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function ban(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setDone("");
    try {
      const res = await withReauth(() => staffClient.banSubdomain({ name, reason }));
      setDone(
        res.orgId
          ? `Banned ${name}. It's gone from the org that had it, and its records go within a minute.`
          : `Banned ${name}. No one had it; now no one can.`,
      );
      setName("");
      setReason("");
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="max-w-xl">
      <CardHeader>
        <CardTitle>Ban a subdomain</CardTitle>
        <CardDescription>
          For names reported for abuse (phishing, malware, impersonation). The org that had it is
          told in its activity log. A banned name can't be claimed again.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form onSubmit={ban} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ban-name">Name</Label>
            <Input
              id="ban-name"
              required
              placeholder="smp.raptornodes.net"
              autoComplete="off"
              spellCheck={false}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ban-reason">Reason</Label>
            <Input
              id="ban-reason"
              required
              maxLength={500}
              placeholder="Phishing page, reported by …"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </div>
          <div>
            <Button type="submit" variant="destructive" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              Ban
            </Button>
          </div>
          {done && <p className="text-sm text-muted-foreground">{done}</p>}
          {error && <p className="text-sm text-destructive">{error}</p>}
        </form>
      </CardContent>
    </Card>
  );
}
