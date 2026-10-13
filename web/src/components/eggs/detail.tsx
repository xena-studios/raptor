import { useNavigate } from "@tanstack/react-router";
import { Archive, Copy, FilePen, ShieldAlert, TriangleAlert } from "lucide-react";
import { lazy, type ReactNode, Suspense, useState } from "react";

import { DetailList, tabClass } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { GetEggDetailsResponse } from "@/gen/raptor/panel/v1/egg_pb";
import {
  access,
  accessNames,
  addonKinds,
  addons,
  knownFeatures,
  loaderName,
  originNames,
} from "@/lib/eggs";
import { message } from "@/lib/errors";
import { useFileSettings } from "@/lib/file-settings";
import { when } from "@/lib/format";
import { eggClient } from "@/lib/transport";

import type { Library } from "./library";

const MonacoEditor = lazy(() => import("@/components/monaco"));

const tabs = [
  ["overview", "Overview"],
  ["startup", "Startup"],
  ["install", "Install"],
  ["config", "Config files"],
  ["file", "File"],
  ["versions", "Versions"],
] as const;
type Tab = (typeof tabs)[number][0];

// EggDetails is what an egg runs, for anyone who can see it: its add-ons,
// runtimes, and settings, then how it starts, installs, and edits config
// files, the file itself, and its versions.
export function EggDetails({ d }: { d: GetEggDetailsResponse }) {
  const [tab, setTab] = useState<Tab>("overview");
  const egg = d.egg;
  const spec = d.spec;
  if (!egg || !spec) return null;
  return (
    <div className="flex flex-col gap-6">
      <Notices d={d} />
      {(egg.summary || spec.description) && (
        <div className="max-w-3xl space-y-2">
          {egg.summary && <p>{egg.summary}</p>}
          {spec.description && (
            <p className="text-sm whitespace-pre-line text-muted-foreground">{spec.description}</p>
          )}
        </div>
      )}
      <nav aria-label="Egg sections" className="-mb-2 flex gap-4 overflow-x-auto border-b">
        {tabs.map(([key, label]) => (
          <button
            key={key}
            type="button"
            className={tabClass}
            data-status={tab === key ? "active" : undefined}
            aria-current={tab === key ? "page" : undefined}
            onClick={() => setTab(key)}
          >
            {label}
          </button>
        ))}
      </nav>
      {tab === "overview" && <Overview d={d} />}
      {tab === "startup" && <Startup d={d} />}
      {tab === "install" && <Install d={d} />}
      {tab === "config" && <ConfigFiles d={d} />}
      {tab === "file" && <FileView d={d} />}
      {tab === "versions" && <Versions d={d} />}
    </div>
  );
}

function Notices({ d }: { d: GetEggDetailsResponse }) {
  const egg = d.egg;
  if (!egg) return null;
  return (
    <>
      {egg.status === "draft" && (
        <Alert>
          <FilePen />
          <AlertTitle>Draft</AlertTitle>
          <AlertDescription>
            {d.canEdit
              ? "Servers can't be made from it yet. Publish it when it's ready."
              : "Servers can't be made from it until it's published."}
          </AlertDescription>
        </Alert>
      )}
      {egg.status === "archived" && (
        <Alert>
          <Archive />
          <AlertTitle>Archived</AlertTitle>
          <AlertDescription>
            It's out of the list, and new servers can't be made from it. Servers made from it keep
            running on their copy.
          </AlertDescription>
        </Alert>
      )}
      {egg.scope === "org" && d.warnings.length > 0 && (
        <Alert>
          <ShieldAlert />
          <AlertTitle>Check what it runs</AlertTitle>
          <AlertDescription>
            <ul className="flex flex-col gap-1">
              {d.warnings.map((w) => (
                <li key={w.kind + w.text} className="flex gap-1.5">
                  <TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-warn" />
                  {w.text}
                </li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
    </>
  );
}

function Section({
  title,
  description,
  children,
}: {
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

function Code({ children, wrap }: { children: ReactNode; wrap?: boolean }) {
  return (
    <pre
      className={`overflow-auto rounded-lg border bg-muted/40 p-3 font-mono text-xs leading-relaxed ${wrap ? "break-all whitespace-pre-wrap" : "max-h-[32rem] whitespace-pre"}`}
    >
      {children}
    </pre>
  );
}

function Overview({ d }: { d: GetEggDetailsResponse }) {
  const egg = d.egg;
  const spec = d.spec;
  if (!egg || !spec) return null;
  const on = [
    ...spec.addons.map((a) => ({
      label: addonKinds[a.kind]?.label ?? a.kind,
      description: `For ${a.loaders.map(loaderName).join(", ")}, in ${a.dir}`,
    })),
    ...(spec.features.includes("eula") ? [addons.eula] : []),
    ...(spec.playersQuery ? [addons.players] : []),
  ];
  const other = spec.features.filter((f) => !knownFeatures.includes(f));
  return (
    <div className="grid items-start gap-6 lg:grid-cols-3">
      <div className="flex flex-col gap-6 lg:col-span-2">
        {(on.length > 0 || other.length > 0) && (
          <Section title="Includes" description="What servers made from this egg get.">
            {on.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {on.map((a) => (
                  <span
                    key={a.label}
                    title={a.description}
                    className="rounded-md border bg-card px-2 py-1 text-sm"
                  >
                    {a.label}
                  </span>
                ))}
              </div>
            )}
            {other.length > 0 && (
              <p className="text-xs text-muted-foreground not-first:mt-3">
                It also asks for features from other panels that Raptor doesn't have:{" "}
                {other.map((f, i) => (
                  <span key={f}>
                    {i > 0 && ", "}
                    <code className="font-mono">{f}</code>
                  </span>
                ))}
                . Servers run without them.
              </p>
            )}
          </Section>
        )}
        <Section
          title="Runtimes"
          description="The image a server runs in; the first is the default."
        >
          <ul className="divide-y rounded-lg border">
            {spec.images.map((i, n) => (
              <li key={i.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5">
                <span className="text-sm font-medium">{i.name}</span>
                {n === 0 && <Badge variant="secondary">Default</Badge>}
                <code className="ml-auto text-xs break-all text-muted-foreground">{i.ref}</code>
              </li>
            ))}
          </ul>
          {d.registries.length > 0 && (
            <p className="mt-2 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
              From
              {d.registries.map((r) => (
                <Badge key={r} variant="outline">
                  {r}
                </Badge>
              ))}
            </p>
          )}
        </Section>
        <Section
          title="Settings"
          description="Variables a server is set up with, and who can change them."
        >
          {spec.variables.length === 0 ? (
            <p className="text-sm text-muted-foreground">None.</p>
          ) : (
            <ul className="divide-y rounded-lg border">
              {spec.variables.map((v) => (
                <li key={v.env} className="flex flex-col gap-1 px-3 py-2.5">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-medium">{v.name}</span>
                    <code className="text-xs text-muted-foreground">{v.env}</code>
                    <Badge variant="outline" className="ml-auto">
                      {accessNames[access(v)].label}
                    </Badge>
                  </div>
                  {v.description && (
                    <p className="text-xs text-muted-foreground">{v.description}</p>
                  )}
                  <p className="flex flex-wrap gap-x-4 font-mono text-xs text-muted-foreground">
                    <span>Default: {v.default || "(empty)"}</span>
                    {v.rules.length > 0 && <span>{v.rules.join(" | ")}</span>}
                  </p>
                </li>
              ))}
            </ul>
          )}
          {d.lint.length > 0 && (
            <p className="mt-2 text-xs text-muted-foreground">
              Rules Raptor skips (it can't check them): {d.lint.join(", ")}
            </p>
          )}
        </Section>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>About</CardTitle>
        </CardHeader>
        <CardContent>
          <DetailList
            rows={[
              ["Origin", originNames[egg.origin] ?? "—"],
              ...(egg.parentName ? [["Based on", egg.parentName] as [string, ReactNode]] : []),
              ["Format", d.format === "pelican" ? "Pelican" : "Pterodactyl"],
              ["Author", spec.author || "—"],
              ["CPUs", spec.arch.length ? spec.arch.join(", ") : "Not said"],
              ["In use", egg.servers === 1 ? "1 server" : `${egg.servers} servers`],
              ...(egg.version > 0 ? [["Version", `v${egg.version}`] as [string, ReactNode]] : []),
              ...(egg.updatedAt ? [["Updated", when(egg.updatedAt)] as [string, ReactNode]] : []),
              ...(egg.createdBy ? [["Made by", egg.createdBy] as [string, ReactNode]] : []),
              ...(egg.sourceUrl
                ? [
                    [
                      "Source",
                      <a
                        key="src"
                        href={egg.sourceUrl}
                        target="_blank"
                        rel="noreferrer"
                        className="underline underline-offset-2"
                      >
                        {new URL(egg.sourceUrl).hostname}
                      </a>,
                    ] as [string, ReactNode],
                  ]
                : []),
            ]}
          />
        </CardContent>
      </Card>
    </div>
  );
}

function Startup({ d }: { d: GetEggDetailsResponse }) {
  const spec = d.spec;
  if (!spec) return null;
  const stop =
    spec.stopType === "command"
      ? `Types ${spec.stopValue}`
      : spec.stopType === "signal"
        ? `Sends ${spec.stopValue}`
        : "The image's own stop signal";
  return (
    <div className="flex flex-col gap-6">
      <Section
        title="Startup command"
        description="{{VARIABLE}} is replaced by the server's setting."
      >
        <div className="flex flex-col gap-3">
          {spec.startup.map((c) => (
            <div key={c.name} className="flex flex-col gap-1">
              {spec.startup.length > 1 && <span className="text-sm font-medium">{c.name}</span>}
              <Code wrap>{c.command}</Code>
            </div>
          ))}
        </div>
      </Section>
      <div className="grid gap-6 md:grid-cols-2">
        <Section title="Ready when" description="A console line that means the server is up.">
          {spec.done.length === 0 ? (
            <p className="text-sm text-muted-foreground">As soon as it starts.</p>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {spec.done.map((l) => (
                <li key={l} className="flex items-center gap-2 text-sm">
                  <Badge variant="outline">{l.startsWith("regex:") ? "Matches" : "Contains"}</Badge>
                  <code className="text-xs break-all">{l.replace(/^regex:/, "")}</code>
                </li>
              ))}
            </ul>
          )}
        </Section>
        <Section title="Stopping">
          <p className="text-sm">{stop}</p>
        </Section>
      </div>
    </div>
  );
}

function Install({ d }: { d: GetEggDetailsResponse }) {
  const spec = d.spec;
  if (!spec) return null;
  return (
    <Section
      title="Install script"
      description={
        spec.installScript.trim()
          ? `Runs once as root in ${spec.installContainer}${spec.installEntrypoint ? ` with ${spec.installEntrypoint}` : ""}, with the server's files in /mnt/server.`
          : "This egg has no install step."
      }
    >
      {spec.installScript.trim() && <Code>{spec.installScript}</Code>}
    </Section>
  );
}

function ConfigFiles({ d }: { d: GetEggDetailsResponse }) {
  const spec = d.spec;
  if (!spec) return null;
  if (spec.files.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        This egg doesn't edit any files before a server starts.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        Wings sets these before every start, with the server's port and settings filled in.
      </p>
      {spec.files.map((f) => (
        <Section key={f.path} title={f.path} description={`Edited as ${f.parser}`}>
          <ul className="divide-y rounded-lg border font-mono text-xs">
            {f.find.map((r) => (
              <li key={r.key + r.ifValue} className="flex flex-wrap gap-x-2 px-3 py-2">
                <span className="text-muted-foreground">{r.key}</span>
                {r.ifValue && <span className="text-muted-foreground">(if {r.ifValue})</span>}
                <span>=</span>
                <span className="break-all">{r.value}</span>
              </li>
            ))}
          </ul>
        </Section>
      ))}
    </div>
  );
}

function FileView({ d }: { d: GetEggDetailsResponse }) {
  const settings = useFileSettings();
  const text = new TextDecoder().decode(d.file);
  const json = text.trimStart().startsWith("{");
  return (
    <Card className="h-[36rem] overflow-hidden py-0">
      <Suspense fallback={<p className="p-4 text-sm text-muted-foreground">Loading the editor…</p>}>
        <MonacoEditor
          value={text}
          language={json ? "json" : "yaml"}
          readOnly
          settings={settings}
          onChange={() => {}}
          onSave={() => {}}
        />
      </Suspense>
    </Card>
  );
}

function Versions({ d }: { d: GetEggDetailsResponse }) {
  if (d.versions.length === 0) {
    return <p className="text-sm text-muted-foreground">No versions yet.</p>;
  }
  return (
    <Card className="py-0">
      <ul className="divide-y">
        {d.versions.map((v) => (
          <li key={v.version} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-4 py-3">
            <span className="font-mono text-sm">v{v.version}</span>
            {v.version === d.egg?.version && <Badge variant="secondary">Current</Badge>}
            <span className="min-w-0 flex-1 text-sm">{v.note}</span>
            <span className="text-xs text-muted-foreground">
              {v.createdBy} · {when(v.createdAt)}
            </span>
          </li>
        ))}
      </ul>
    </Card>
  );
}

// CustomizeButton copies an egg into the library, as a draft to edit:
// "Customize" for someone else's, "Duplicate" for the library's own.
export function CustomizeButton({ lib, id, label }: { lib: Library; id: string; label: string }) {
  const navigate = useNavigate();
  const withReauth = useReauth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <span className="flex items-center gap-2">
      {error && <span className="text-sm text-destructive">{error}</span>}
      <Button
        variant="outline"
        disabled={busy}
        onClick={async () => {
          setBusy(true);
          setError("");
          try {
            const res = await withReauth(() => eggClient.customizeEgg({ orgId: lib.orgId, id }));
            navigate(lib.edit(res.id));
          } catch (err) {
            setError(message(err));
          } finally {
            setBusy(false);
          }
        }}
      >
        <Copy /> {label}
      </Button>
    </span>
  );
}
