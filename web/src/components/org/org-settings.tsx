import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useState } from "react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { message } from "@/lib/errors";
import { orgClient } from "@/lib/transport";

// OrgSettings renames the org (admins and owners).
export function OrgSettings({ orgId, name: current }: { orgId: string; name: string }) {
  const client = useQueryClient();
  const [name, setName] = useState(current);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  async function rename(e: FormEvent) {
    e.preventDefault();
    setError("");
    setSaved(false);
    try {
      await orgClient.renameOrg({ orgId, name });
      await client.invalidateQueries();
      setSaved(true);
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Name</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={rename} className="flex gap-2">
          <Input
            aria-label="Org name"
            required
            maxLength={64}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <Button type="submit" disabled={name.trim() === current}>
            Rename
          </Button>
        </form>
        {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
        {saved && <p className="mt-2 text-sm text-muted-foreground">Renamed.</p>}
      </CardContent>
    </Card>
  );
}
