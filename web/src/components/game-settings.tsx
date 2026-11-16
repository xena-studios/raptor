import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Loader2, RotateCcw } from "lucide-react";
import { useState } from "react";

import { type Content, useFilesApi } from "@/components/file-manager/data";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { message } from "@/lib/errors";
import { fromBase64 } from "@/lib/files";
import {
  applyValues,
  type Field,
  fieldValue,
  formFields,
  invalid,
  minecraftManaged,
  otherKeys,
} from "@/lib/game-settings";
import { propertyValue } from "@/lib/minecraft-text";
import { base64 } from "@/lib/servers";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

const sections = ["Gameplay", "Players", "World", "Performance"] as const;

// GameSettings is a Minecraft server's server.properties as a form (#424):
// each setting with what it does and its default, written back in place
// so the rest of the file stays as it is. Settings Raptor manages show
// where they're changed; ones the form doesn't know are listed, and the
// file is a click away for anything else.
export function GameSettings({
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
  const api = useFilesApi(nodeId, serverId);
  const file = useTanQuery({
    queryKey: api.contentKey("server.properties"),
    queryFn: () => api.run<Content>("files.read", { path: "server.properties" }),
    retry: false,
    refetchOnWindowFocus: false,
  });
  if (file.isPending) return <p className="text-sm text-muted-foreground">Loading its settings…</p>;
  if (!file.data?.data) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Game settings</CardTitle>
          <CardDescription>
            There's no server.properties yet. Start the server once: Minecraft makes it on its first
            start, and its settings show here.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }
  const text = new TextDecoder().decode(fromBase64(file.data.data));
  return (
    <SettingsForm
      key={file.data.data}
      text={text}
      orgId={orgId}
      serverId={serverId}
      canWrite={canWrite}
      api={api}
    />
  );
}

function SettingsForm({
  text,
  orgId,
  serverId,
  canWrite,
  api,
}: {
  text: string;
  orgId: string;
  serverId: string;
  canWrite: boolean;
  api: ReturnType<typeof useFilesApi>;
}) {
  const client = useQueryClient();
  const fields = formFields(text);
  const initial = Object.fromEntries(fields.map((f) => [f.key, fieldValue(text, f) ?? ""]));
  const [values, setValues] = useState<Record<string, string>>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const changed = Object.fromEntries(Object.entries(values).filter(([k, v]) => v !== initial[k]));
  const problems = fields.flatMap((f) => {
    const p = invalid(f, values[f.key] ?? "");
    return p ? [[f.key, p] as const] : [];
  });
  const other = otherKeys(text);
  const managed = Object.entries(minecraftManaged).filter(
    ([k]) => propertyValue(text, k) !== undefined,
  );

  async function save() {
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      await api.run("files.write", {
        path: "server.properties",
        data: base64(new TextEncoder().encode(applyValues(text, changed))),
      });
      await client.invalidateQueries({ queryKey: api.contentKey("server.properties") });
      setSaved(true);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const set = (k: string, v: string) => {
    setValues({ ...values, [k]: v });
    setSaved(false);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Game settings</CardTitle>
        <CardDescription>
          Minecraft's settings (server.properties). Changes take effect when the server restarts.{" "}
          <Link
            to="/orgs/$orgId/servers/$serverId/files"
            params={{ orgId, serverId }}
            search={{ file: "server.properties" }}
            className="underline underline-offset-4"
          >
            Edit as text
          </Link>
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {sections.map((section) => {
          const list = fields.filter((f) => f.section === section);
          if (list.length === 0) return null;
          return (
            <fieldset key={section} className="flex flex-col gap-4">
              <legend className="mb-2 text-sm font-medium">{section}</legend>
              <div className="grid gap-x-6 gap-y-4 md:grid-cols-2">
                {list.map((f) => (
                  <FieldInput
                    key={f.key}
                    field={f}
                    value={values[f.key] ?? ""}
                    problem={problems.find(([k]) => k === f.key)?.[1]}
                    disabled={!canWrite}
                    onChange={(v) => set(f.key, v)}
                  />
                ))}
              </div>
            </fieldset>
          );
        })}
        {(managed.length > 0 || other.length > 0) && (
          <div className="flex flex-col gap-2 text-xs text-muted-foreground">
            {managed.length > 0 && (
              <p>
                {managed.map(([k]) => k).join(", ")}: set by Raptor at every start; change the port
                on Network.
              </p>
            )}
            {other.length > 0 && (
              <p>Also in the file, left as they are: {other.join(", ")}. Edit them as text.</p>
            )}
          </div>
        )}
        {canWrite && (
          <div className="flex flex-wrap items-center gap-3">
            <Button
              disabled={busy || Object.keys(changed).length === 0 || problems.length > 0}
              onClick={() => void save()}
            >
              {busy && <Loader2 className="animate-spin" />} Save
            </Button>
            {Object.keys(changed).length > 0 && (
              <Button variant="ghost" disabled={busy} onClick={() => setValues(initial)}>
                Undo changes
              </Button>
            )}
            {saved && (
              <span className="text-sm text-muted-foreground">
                Saved. Restart the server to use them.
              </span>
            )}
          </div>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}

function FieldInput({
  field: f,
  value,
  problem,
  disabled,
  onChange,
}: {
  field: Field;
  value: string;
  problem?: string;
  disabled: boolean;
  onChange: (v: string) => void;
}) {
  const id = `gs-${f.key}`;
  const def = String(f.default);
  const reset =
    !disabled && value !== def ? (
      <Button
        type="button"
        size="icon-xs"
        variant="ghost"
        title={`Back to the default (${def || "empty"})`}
        aria-label={`Reset ${f.label}`}
        className="text-muted-foreground"
        onClick={() => onChange(def)}
      >
        <RotateCcw />
      </Button>
    ) : null;
  if (f.type === "bool") {
    return (
      <div className="flex items-start justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <Label htmlFor={id}>{f.label}</Label>
          <p className="text-xs text-muted-foreground">{f.help}</p>
        </div>
        <div className="flex items-center gap-1">
          {reset}
          <Switch
            id={id}
            checked={value === "true"}
            disabled={disabled}
            onCheckedChange={(on) => onChange(on ? "true" : "false")}
          />
        </div>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={id}>{f.label}</Label>
        {reset}
      </div>
      {f.type === "choice" ? (
        <select
          id={id}
          className={select}
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        >
          {!f.choices.some(([v]) => v === value) && <option value={value}>{value}</option>}
          {f.choices.map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
      ) : (
        <Input
          id={id}
          type={f.type === "number" ? "number" : "text"}
          min={f.type === "number" ? f.min : undefined}
          max={f.type === "number" ? f.max : undefined}
          value={value}
          disabled={disabled}
          aria-invalid={!!problem || undefined}
          spellCheck={false}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      <p className={problem ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
        {problem ?? f.help}
      </p>
    </div>
  );
}
