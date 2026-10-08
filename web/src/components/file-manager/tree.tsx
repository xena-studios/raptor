import { ChevronDown, ChevronRight, Folder, FolderOpen, Loader2 } from "lucide-react";
import { useState } from "react";

import { join } from "@/lib/files";
import { cn } from "@/lib/utils";
import { type Entry, kind, sorted, type useFilesApi, useListing } from "./data";
import { EntryIcon } from "./icons";

type Api = ReturnType<typeof useFilesApi>;

// Tree is the server's folders and files, each folder listed when it's
// opened. The folders on the way to the current one start open.
export function Tree({
  api,
  current,
  openFile,
  onFolder,
  onFile,
}: {
  api: Api;
  current: string;
  openFile?: string;
  onFolder: (path: string) => void;
  onFile: (path: string, e: Entry) => void;
}) {
  return (
    <ul className="py-1 text-sm">
      <Node
        api={api}
        name="Server root"
        path=""
        depth={0}
        current={current}
        openFile={openFile}
        onFolder={onFolder}
        onFile={onFile}
      />
    </ul>
  );
}

function onTheWay(path: string, current: string) {
  return path === "" || current === path || current.startsWith(`${path}/`);
}

function Node({
  api,
  name,
  path,
  depth,
  current,
  openFile,
  onFolder,
  onFile,
}: {
  api: Api;
  name: string;
  path: string;
  depth: number;
  current: string;
  openFile?: string;
  onFolder: (path: string) => void;
  onFile: (path: string, e: Entry) => void;
}) {
  // Open if the user opened it, or it's on the way to where they are.
  const [toggled, setToggled] = useState<boolean>();
  const open = toggled ?? onTheWay(path, current);
  const listing = useListing(api, path, open);
  const active = current === path && !openFile;
  const pad = { paddingLeft: `${depth * 14 + 8}px` };

  return (
    <li>
      <div
        className={cn(
          "group flex h-7 items-center gap-1 rounded-md pr-2 hover:bg-muted",
          active && "bg-muted font-medium",
        )}
        style={pad}
      >
        <button
          type="button"
          aria-label={open ? `Close ${name}` : `Open ${name}`}
          className="flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground"
          onClick={() => setToggled(!open)}
        >
          {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        </button>
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          onClick={() => {
            onFolder(path);
            if (!open) setToggled(true);
          }}
        >
          {open ? (
            <FolderOpen className="size-4 shrink-0 text-sky-500" />
          ) : (
            <Folder className="size-4 shrink-0 text-sky-500" />
          )}
          <span className="truncate">{name}</span>
        </button>
      </div>
      {open && (
        <ul>
          {listing.isPending && (
            <li className="flex h-7 items-center" style={{ paddingLeft: `${depth * 14 + 36}px` }}>
              <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
            </li>
          )}
          {listing.error && (
            <li
              className="h-7 truncate text-xs leading-7 text-destructive"
              style={{ paddingLeft: `${depth * 14 + 36}px` }}
            >
              Couldn't list this folder
            </li>
          )}
          {sorted(listing.data?.entries ?? []).map((e) => {
            const p = join(path, e.name);
            if (kind(e) === "dir" && !e.denied) {
              return (
                <Node
                  key={e.name}
                  api={api}
                  name={e.name}
                  path={p}
                  depth={depth + 1}
                  current={current}
                  openFile={openFile}
                  onFolder={onFolder}
                  onFile={onFile}
                />
              );
            }
            return (
              <li key={e.name}>
                <button
                  type="button"
                  disabled={e.denied || kind(e) !== "file"}
                  title={e.denied ? "The game's egg doesn't allow opening this" : undefined}
                  className={cn(
                    "flex h-7 w-full items-center gap-2 rounded-md pr-2 text-left hover:bg-muted disabled:opacity-50 disabled:hover:bg-transparent",
                    openFile === p && "bg-muted font-medium",
                  )}
                  style={{ paddingLeft: `${(depth + 1) * 14 + 8 + 24}px` }}
                  onClick={() => onFile(p, e)}
                >
                  <EntryIcon e={e} />
                  <span className="truncate">{e.name}</span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </li>
  );
}
