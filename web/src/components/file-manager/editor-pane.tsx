import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Download, Loader2, Save, X } from "lucide-react";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";

import { Button, buttonVariants } from "@/components/ui/button";
import { message } from "@/lib/errors";
import { loadDraft, saveDraft, useFileSettings } from "@/lib/file-settings";
import {
  baseName,
  fromBase64,
  languageFor,
  languageName,
  looksBinary,
  readOnlyByName,
} from "@/lib/files";
import { base64 } from "@/lib/servers";
import { downloadURL } from "@/lib/upload";
import type { Content, useFilesApi } from "./data";

const MonacoEditor = lazy(() => import("@/components/monaco"));

const autoSaveDelay = 3_000;

type Status = "clean" | "dirty" | "saving" | "saved" | "error";

// EditorPane edits one file: Monaco, a Save button, and a status bar.
// Unsaved text is kept in this browser as a draft until it's saved.
export function EditorPane({
  api,
  nodeId,
  serverId,
  path,
  canWrite,
  onBack,
  onClose,
  onDirty,
}: {
  api: ReturnType<typeof useFilesApi>;
  nodeId: string;
  serverId: string;
  path: string;
  canWrite: boolean;
  onBack: () => void;
  onClose: () => void;
  onDirty: (dirty: boolean) => void;
}) {
  const settings = useFileSettings();
  // Read once: saving doesn't reload the file, which would reset the editor.
  const file = useQuery({
    queryKey: ["file", nodeId, serverId, path],
    queryFn: () => api.run<Content>("files.read", { path }),
    gcTime: 0,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
  });
  const loaded = useMemo(() => {
    const data = file.data?.data ? fromBase64(file.data.data) : new Uint8Array();
    return { binary: looksBinary(data), text: new TextDecoder().decode(data) };
  }, [file.data]);
  const binary = file.data !== undefined && loaded.binary;
  const readOnly = !canWrite || readOnlyByName(path);
  const language = languageFor(baseName(path));

  // What's on the server, and what's in the editor.
  const [saved, setSaved] = useState<string>();
  const [text, setText] = useState<string>();
  const [initial, setInitial] = useState<string>();
  const [restored, setRestored] = useState(false);
  const [status, setStatus] = useState<Status>("clean");
  const [error, setError] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);

  // When the file arrives, start from its draft if this browser has one.
  useEffect(() => {
    if (!file.data || binary) return;
    const draft = readOnly ? null : loadDraft(nodeId, serverId, path);
    const useDraft = draft !== null && draft !== loaded.text;
    setSaved(loaded.text);
    setText(useDraft ? draft : loaded.text);
    setInitial(useDraft ? draft : loaded.text);
    setRestored(useDraft);
    setStatus(useDraft ? "dirty" : "clean");
  }, [file.data, binary, loaded.text, readOnly, nodeId, serverId, path]);

  const dirty = text !== undefined && saved !== undefined && text !== saved;
  useEffect(() => onDirty(dirty), [dirty, onDirty]);

  async function save(value = text) {
    if (readOnly || value === undefined) return;
    clearTimeout(timer.current);
    setStatus("saving");
    setError("");
    try {
      await api.run("files.write", { path, data: base64(new TextEncoder().encode(value)) });
      setSaved(value);
      saveDraft(nodeId, serverId, path, null);
      setRestored(false);
      setStatus("saved");
    } catch (err) {
      setError(message(err));
      setStatus("error");
    }
  }

  function change(value: string) {
    setText(value);
    setStatus(value === saved ? "clean" : "dirty");
    saveDraft(nodeId, serverId, path, value === saved ? null : value);
    clearTimeout(timer.current);
    if (settings.autoSave && value !== saved) {
      timer.current = setTimeout(() => void save(value), autoSaveDelay);
    }
  }
  useEffect(() => () => clearTimeout(timer.current), []);

  function discardDraft() {
    saveDraft(nodeId, serverId, path, null);
    setInitial(saved);
    setText(saved);
    setRestored(false);
    setStatus("clean");
  }

  const statusText = {
    clean: "No changes",
    dirty: settings.autoSave ? "Unsaved changes, saving soon" : "Unsaved changes",
    saving: "Saving…",
    saved: "Saved",
    error: "Couldn't save",
  }[status];

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <Button size="icon-sm" variant="ghost" aria-label="Back to the folder" onClick={onBack}>
          <ArrowLeft />
        </Button>
        <span className="truncate text-sm font-medium">{baseName(path)}</span>
        <span className="hidden truncate text-xs text-muted-foreground sm:inline">/{path}</span>
        {readOnly && canWrite && (
          <span className="rounded border px-1.5 text-xs text-muted-foreground">Read-only</span>
        )}
        <div className="ml-auto flex items-center gap-1">
          <a
            href={downloadURL(nodeId, serverId, path)}
            aria-label="Download"
            title="Download"
            className={buttonVariants({ size: "icon-sm", variant: "ghost" })}
          >
            <Download />
          </a>
          {!readOnly && !binary && (
            <Button size="sm" disabled={!dirty || status === "saving"} onClick={() => void save()}>
              {status === "saving" ? <Loader2 className="animate-spin" /> : <Save />} Save
            </Button>
          )}
          <Button size="icon-sm" variant="ghost" aria-label="Close the file" onClick={onClose}>
            <X />
          </Button>
        </div>
      </div>
      {restored && (
        <div className="flex shrink-0 items-center gap-3 border-b bg-muted/50 px-4 py-1.5 text-xs">
          <span>Restored your unsaved changes from this browser.</span>
          <button type="button" className="underline" onClick={discardDraft}>
            Discard them
          </button>
        </div>
      )}
      {error && <p className="shrink-0 border-b px-4 py-1.5 text-xs text-destructive">{error}</p>}
      <div className="min-h-0 flex-1">
        {file.isPending && (
          <p className="p-4 text-sm text-muted-foreground">Opening {baseName(path)}…</p>
        )}
        {file.error && <p className="p-4 text-sm text-destructive">{message(file.error)}</p>}
        {binary && (
          <div className="flex flex-col items-start gap-3 p-4 text-sm text-muted-foreground">
            This isn't a text file, so it can't be edited here.
            <a
              href={downloadURL(nodeId, serverId, path)}
              className={buttonVariants({ size: "sm", variant: "outline" })}
            >
              <Download /> Download it
            </a>
          </div>
        )}
        {initial !== undefined && !binary && (
          <Suspense
            fallback={<p className="p-4 text-sm text-muted-foreground">Loading the editor…</p>}
          >
            <MonacoEditor
              value={initial}
              language={language}
              readOnly={readOnly}
              settings={settings}
              onChange={change}
              onSave={() => void save()}
            />
          </Suspense>
        )}
      </div>
      <div className="flex h-7 shrink-0 items-center justify-between border-t px-4 text-xs text-muted-foreground">
        <span>{languageName(language)}</span>
        <span>{readOnly ? "Read-only" : statusText} · F1 for commands</span>
      </div>
    </div>
  );
}
