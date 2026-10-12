import { create } from "@bufbuild/protobuf";
import { type FormEvent, useState } from "react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
import { type EggVariable, EggVariableSchema } from "@/gen/raptor/panel/v1/catalog_pb";
import {
  type Access,
  access,
  accessNames,
  kindNames,
  readRules,
  type VariableForm,
  type VariableKind,
  withAccess,
  writeRules,
} from "@/lib/eggs";

export const field =
  "w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";
export const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

const envName = /^[A-Za-z_][A-Za-z0-9_]*$/;
const reserved = [
  "STARTUP",
  "SERVER_MEMORY",
  "SERVER_IP",
  "SERVER_PORT",
  "TZ",
  "HOME",
  "USER",
  "PATH",
];

// VariableDialog adds or edits one variable: a name and key, a kind (which
// writes its rules), who can see and change it, and its default.
export function VariableDialog({
  initial,
  taken,
  onSave,
  onClose,
}: {
  initial?: EggVariable;
  // Keys other variables use.
  taken: string[];
  onSave: (v: EggVariable) => void;
  onClose: () => void;
}) {
  const [v, setV] = useState<EggVariable>(
    initial ??
      create(EggVariableSchema, {
        userViewable: true,
        userEditable: true,
        rules: ["nullable", "string"],
      }),
  );
  const [form, setForm] = useState<VariableForm>(() => readRules(v.rules));
  const [rules, setRules] = useState(v.rules.join("|"));
  const [error, setError] = useState("");

  function change(next: Partial<VariableForm>) {
    const f = { ...form, ...next };
    setForm(f);
    if (f.kind !== "custom") setRules(writeRules(f).join("|"));
  }

  function save(e: FormEvent) {
    e.preventDefault();
    const env = v.env.trim();
    if (!v.name.trim()) return setError("Name it.");
    if (!envName.test(env)) return setError("The key is letters, digits, and _, like SERVER_NAME.");
    if (reserved.includes(env.toUpperCase()))
      return setError(`${env} is set by Raptor; choose another key.`);
    if (taken.includes(env)) return setError(`Another variable uses ${env}.`);
    if (form.kind === "choice" && form.options.filter((o) => o.trim()).length === 0)
      return setError("Add the choices.");
    onSave({
      ...v,
      name: v.name.trim(),
      env,
      rules: rules
        .split("|")
        .map((r) => r.trim())
        .filter(Boolean),
    });
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={save} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{initial ? "Edit variable" : "Add a variable"}</DialogTitle>
            <DialogDescription>
              A setting the server gets as an environment variable, and in the startup command as{" "}
              {"{{KEY}}"}.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-1.5">
              <Label htmlFor="var-name">Name</Label>
              <Input
                id="var-name"
                autoFocus
                placeholder="Server name"
                value={v.name}
                onChange={(e) => setV({ ...v, name: e.target.value })}
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="var-env">Key</Label>
              <Input
                id="var-env"
                className="font-mono"
                placeholder="SERVER_NAME"
                value={v.env}
                onChange={(e) =>
                  setV({ ...v, env: e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_") })
                }
              />
            </div>
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="var-desc">Description</Label>
            <textarea
              id="var-desc"
              rows={2}
              className={field}
              placeholder="Shown next to the setting."
              value={v.description}
              onChange={(e) => setV({ ...v, description: e.target.value })}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-1.5">
              <Label htmlFor="var-kind">Kind</Label>
              <select
                id="var-kind"
                className={select}
                value={form.kind}
                onChange={(e) => change({ kind: e.target.value as VariableKind })}
              >
                {Object.entries(kindNames).map(([k, label]) => (
                  <option key={k} value={k}>
                    {label}
                  </option>
                ))}
              </select>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="var-access">Who can change it</Label>
              <select
                id="var-access"
                className={select}
                value={access(v)}
                onChange={(e) => setV(withAccess(v, e.target.value as Access))}
              >
                {Object.entries(accessNames).map(([k, a]) => (
                  <option key={k} value={k}>
                    {a.label}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">{accessNames[access(v)].hint}.</p>
            </div>
          </div>
          {form.kind === "choice" && (
            <div className="grid gap-1.5">
              <Label htmlFor="var-options">Choices</Label>
              <Input
                id="var-options"
                placeholder="peaceful, easy, normal, hard"
                value={form.options.join(", ")}
                onChange={(e) =>
                  change({ options: e.target.value.split(",").map((o) => o.trimStart()) })
                }
              />
            </div>
          )}
          {(form.kind === "text" || form.kind === "number") && (
            <div className="grid gap-4 sm:grid-cols-2">
              {form.kind === "number" && (
                <div className="grid gap-1.5">
                  <Label htmlFor="var-min">Lowest</Label>
                  <Input
                    id="var-min"
                    inputMode="numeric"
                    value={form.min}
                    onChange={(e) => change({ min: e.target.value.replace(/[^0-9-]/g, "") })}
                  />
                </div>
              )}
              <div className="grid gap-1.5">
                <Label htmlFor="var-max">{form.kind === "number" ? "Highest" : "Longest"}</Label>
                <Input
                  id="var-max"
                  inputMode="numeric"
                  placeholder={form.kind === "text" ? "No limit" : ""}
                  value={form.max}
                  onChange={(e) => change({ max: e.target.value.replace(/[^0-9-]/g, "") })}
                />
              </div>
            </div>
          )}
          {form.kind !== "custom" && (
            <Label className="flex items-center gap-2 text-sm font-normal">
              <Checkbox
                checked={form.required}
                onCheckedChange={(c) => change({ required: c === true })}
              />
              Required
            </Label>
          )}
          <div className="grid gap-1.5">
            <Label htmlFor="var-default">Default</Label>
            {form.kind === "toggle" ? (
              <select
                id="var-default"
                className={select}
                value={v.default}
                onChange={(e) => setV({ ...v, default: e.target.value })}
              >
                <option value="1">On</option>
                <option value="0">Off</option>
              </select>
            ) : form.kind === "choice" ? (
              <select
                id="var-default"
                className={select}
                value={v.default}
                onChange={(e) => setV({ ...v, default: e.target.value })}
              >
                <option value="">(none)</option>
                {form.options
                  .map((o) => o.trim())
                  .filter(Boolean)
                  .map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
              </select>
            ) : (
              <Input
                id="var-default"
                value={v.default}
                onChange={(e) => setV({ ...v, default: e.target.value })}
              />
            )}
          </div>
          <div className="grid gap-1.5">
            <Label htmlFor="var-rules">Rules</Label>
            <Input
              id="var-rules"
              className="font-mono text-xs"
              value={rules}
              readOnly={form.kind !== "custom"}
              onChange={(e) => setRules(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              {form.kind === "custom"
                ? "Laravel validation rules, like required|string|max:20. Raptor checks them before a server starts."
                : "Written from the choices above. Choose Custom rules to write your own."}
            </p>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit">{initial ? "Done" : "Add"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
