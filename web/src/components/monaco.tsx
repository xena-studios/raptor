import "monaco-editor/editor/editor.main.js";

import * as monaco from "monaco-editor/editor/editor.api.js";
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";
import JsonWorker from "monaco-editor/language/json/json.worker.js?worker";
import { useEffect, useMemo, useRef } from "react";

import type { EditorTheme, FileSettings } from "@/lib/file-settings";
import { useTheme } from "@/lib/theme";

// Monaco's workers are our own files (worker-src 'self' in the CSP). JSON
// gets its own, for validation and folding; everything else uses the
// editor's.
self.MonacoEnvironment = {
  getWorker: (_id: string, label: string) =>
    label === "json" ? new JsonWorker() : new EditorWorker(),
};

// cssColor is a CSS variable as hex, for Monaco (the app's colors are
// oklch, which Monaco can't read): drawn on a canvas and read back.
function cssColor(variable: string): string {
  const probe = document.createElement("div");
  probe.style.color = `var(${variable})`;
  document.body.append(probe);
  const color = getComputedStyle(probe).color;
  probe.remove();
  const ctx = document.createElement("canvas").getContext("2d");
  if (!ctx) return "#000000";
  ctx.fillStyle = color;
  ctx.fillRect(0, 0, 1, 1);
  const [r = 0, g = 0, b = 0] = ctx.getImageData(0, 0, 1, 1).data;
  return `#${[r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("")}`;
}

// "Match Raptor" is Monaco's own dark or light theme on the card's colors.
function resolveTheme(choice: EditorTheme, app: string): string {
  if (choice === "dark") return "vs-dark";
  if (choice === "light") return "vs";
  if (choice !== "auto") return choice;
  const dark =
    app === "dark" ||
    (app === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  const name = dark ? "raptor-dark" : "raptor-light";
  const background = cssColor("--card");
  monaco.editor.defineTheme(name, {
    base: dark ? "vs-dark" : "vs",
    inherit: true,
    rules: [],
    colors: {
      "editor.background": background,
      "editorGutter.background": background,
      "editorStickyScroll.background": background,
      "minimap.background": background,
    },
  });
  return name;
}

function options(s: FileSettings): monaco.editor.IEditorOptions {
  return {
    wordWrap: s.wordWrap ? "on" : "off",
    stickyScroll: { enabled: s.stickyScroll },
    guides: { indentation: !s.hideIndentGuides },
    quickSuggestions: !s.hideAutocomplete,
    suggestOnTriggerCharacters: !s.hideAutocomplete,
  };
}

// MonacoEditor edits one file. Loaded lazily, with the editor pane only.
// Ctrl/Cmd-S saves.
export default function MonacoEditor({
  value,
  language,
  readOnly,
  settings,
  onChange,
  onSave,
}: {
  value: string;
  language: string;
  readOnly: boolean;
  settings: FileSettings;
  onChange: (text: string) => void;
  onSave: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const editor = useRef<monaco.editor.IStandaloneCodeEditor>(null);
  const handlers = useRef({ onChange, onSave });
  handlers.current = { onChange, onSave };
  const settingsAtStart = useRef(settings);
  settingsAtStart.current = settings;
  const appTheme = useTheme();
  const theme = useMemo(
    () => resolveTheme(settings.editorTheme, appTheme),
    [settings.editorTheme, appTheme],
  );

  // One editor per file: a new file gets a new model and a fresh undo
  // history.
  useEffect(() => {
    if (!box.current) return;
    const model = monaco.editor.createModel(value, language);
    const ed = monaco.editor.create(box.current, {
      model,
      readOnly,
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 13,
      scrollBeyondLastLine: false,
      renderLineHighlight: "line",
      tabSize: 2,
      // Later changes are applied below, without a new editor.
      ...options(settingsAtStart.current),
    });
    editor.current = ed;
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => handlers.current.onSave());
    const sub = model.onDidChangeContent(() => handlers.current.onChange(model.getValue()));
    ed.focus();
    return () => {
      sub.dispose();
      ed.dispose();
      model.dispose();
      editor.current = null;
    };
  }, [value, language, readOnly]);

  useEffect(() => {
    editor.current?.updateOptions(options(settings));
  }, [settings]);

  useEffect(() => {
    monaco.editor.setTheme(theme);
  }, [theme]);

  return <div ref={box} className="h-full min-h-0 w-full" />;
}
