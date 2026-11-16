import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useInfiniteQuery, useQuery } from "@connectrpc/connect-query";
import { ExternalLink, Loader2, Package, Search } from "lucide-react";
import { useDeferredValue, useState } from "react";

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
import { type AddonProject, AddonService } from "@/gen/raptor/panel/v1/addon_pb";
import { message } from "@/lib/errors";
import { dateOnly } from "@/lib/format";

// Pick is what the picker sets the egg's variables to: the pack's slug,
// a version's ID (or latest), and a game version that version is for.
export type Pick = { project: string; version: string; gameVersion: string; title: string };

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

function compact(n: bigint | number): string {
  return Intl.NumberFormat(undefined, { notation: "compact" }).format(Number(n));
}

// ModpackButton opens the pack picker (#413) for a modpack egg: search
// Modrinth's modpacks, pick a version, and the egg's variables are filled
// in. The egg's install script downloads the pack, as it would from IDs
// typed by hand.
export function ModpackButton({
  orgId,
  current,
  onPick,
  disabled,
}: {
  orgId: string;
  // The pack the variables name now, if any.
  current?: string;
  onPick: (p: Pick) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        className="self-start"
        disabled={disabled}
        onClick={() => setOpen(true)}
      >
        <Package /> {current ? "Choose another modpack" : "Browse modpacks"}
      </Button>
      {open && (
        <ModpackPicker
          orgId={orgId}
          onClose={() => setOpen(false)}
          onPick={(p) => {
            onPick(p);
            setOpen(false);
          }}
        />
      )}
    </>
  );
}

function ModpackPicker({
  orgId,
  onClose,
  onPick,
}: {
  orgId: string;
  onClose: () => void;
  onPick: (p: Pick) => void;
}) {
  const [query, setQuery] = useState("");
  const [pack, setPack] = useState<AddonProject>();
  const q = useDeferredValue(query.trim());
  const search = useInfiniteQuery(
    AddonService.method.searchAddons,
    { orgId, kind: "modpacks", loaders: [], query: q, offset: 0, source: "modrinth" },
    {
      pageParamKey: "offset",
      getNextPageParam: (last, pages) => {
        const seen = pages.reduce((n, p) => n + p.projects.length, 0);
        return seen < last.total && last.projects.length > 0 ? seen : undefined;
      },
    },
  );
  const packs = search.data?.pages.flatMap((p) => p.projects) ?? [];
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{pack ? pack.title : "Choose a modpack"}</DialogTitle>
          <DialogDescription>
            {pack
              ? "Pick a version. The server downloads it from Modrinth when it installs."
              : "Modpacks from Modrinth, most downloaded first."}
          </DialogDescription>
        </DialogHeader>
        {pack ? (
          <Versions orgId={orgId} pack={pack} onBack={() => setPack(undefined)} onPick={onPick} />
        ) : (
          <div className="flex flex-col gap-3">
            <div className="relative">
              <Search className="pointer-events-none absolute top-2 left-2.5 size-4 text-muted-foreground" />
              <Input
                type="search"
                aria-label="Search modpacks"
                placeholder="Search modpacks…"
                className="pl-8"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
            {search.error && <p className="text-sm text-destructive">{message(search.error)}</p>}
            {search.data && packs.length === 0 && (
              <p className="text-sm text-muted-foreground">Nothing matches.</p>
            )}
            <ul className="flex flex-col gap-2">
              {packs.map((p) => (
                <li key={p.id}>
                  <button
                    type="button"
                    onClick={() => setPack(p)}
                    className="flex w-full items-start gap-3 rounded-lg border p-3 text-left hover:bg-muted/50"
                  >
                    {p.iconUrl ? (
                      <img
                        src={p.iconUrl}
                        alt=""
                        className="size-10 shrink-0 rounded-lg border object-cover"
                        loading="lazy"
                      />
                    ) : (
                      <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                        <Package className="size-5" />
                      </span>
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{p.title}</span>
                      <span className="block text-xs text-muted-foreground">
                        by {p.author} · {compact(p.downloads)} downloads
                      </span>
                      <span className="mt-1 line-clamp-2 block text-xs text-muted-foreground">
                        {p.description}
                      </span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
            {search.hasNextPage && (
              <Button
                variant="outline"
                className="self-center"
                disabled={search.isFetchingNextPage}
                onClick={() => search.fetchNextPage()}
              >
                {search.isFetchingNextPage && <Loader2 className="animate-spin" />} Show more
              </Button>
            )}
          </div>
        )}
        {!pack && (
          <DialogFooter>
            <Button variant="outline" onClick={onClose}>
              Cancel
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

function Versions({
  orgId,
  pack,
  onBack,
  onPick,
}: {
  orgId: string;
  pack: AddonProject;
  onBack: () => void;
  onPick: (p: Pick) => void;
}) {
  const versions = useQuery(AddonService.method.listAddonVersions, {
    orgId,
    projectId: pack.slug || pack.id,
    kind: "modpacks",
    loaders: [],
  });
  const list = versions.data?.versions ?? [];
  // "latest": the newest release whenever the server reinstalls.
  const [chosen, setChosen] = useState("latest");
  const v = list.find((x) => x.id === chosen) ?? list[0];
  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        {pack.description}{" "}
        <a
          href={pack.url}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 underline underline-offset-4"
        >
          On Modrinth <ExternalLink className="size-3" />
        </a>
      </p>
      {versions.error ? (
        <p className="text-sm text-destructive">{message(versions.error)}</p>
      ) : !versions.data ? (
        <p className="text-sm text-muted-foreground">Finding versions…</p>
      ) : list.length === 0 ? (
        <p className="text-sm text-muted-foreground">This pack has no versions to install.</p>
      ) : (
        <div className="grid gap-1.5">
          <Label htmlFor="pack-version">Version</Label>
          <select
            id="pack-version"
            className={select}
            value={chosen}
            onChange={(e) => setChosen(e.target.value)}
          >
            <option value="latest">
              The newest, {list[0]?.versionNumber} (updated when you reinstall)
            </option>
            {list.map((x) => (
              <option key={x.id} value={x.id}>
                {x.versionNumber} · Minecraft {x.gameVersions.join(", ")} · {x.loaders.join(", ")}
                {x.publishedAt ? ` · ${dateOnly(timestampDate(x.publishedAt))}` : ""}
              </option>
            ))}
          </select>
          {v && (
            <p className="text-xs text-muted-foreground">
              Minecraft {v.gameVersions.join(", ")} on {v.loaders.join(", ")}. Make sure the server
              has enough memory: big packs want 6 GB or more.
            </p>
          )}
        </div>
      )}
      <DialogFooter>
        <Button variant="outline" onClick={onBack}>
          Back
        </Button>
        <Button
          disabled={!v}
          onClick={() =>
            v &&
            onPick({
              project: pack.slug || pack.id,
              version: chosen === "latest" ? "latest" : v.id,
              // A specific version needs a game version it's for (the egg
              // filters by it); latest follows any.
              gameVersion: chosen === "latest" ? "latest" : (v.gameVersions.at(-1) ?? "latest"),
              title: pack.title,
            })
          }
        >
          Use this pack
        </Button>
      </DialogFooter>
    </div>
  );
}
