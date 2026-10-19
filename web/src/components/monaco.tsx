import "monaco-editor/editor/editor.main.js";

import * as monaco from "monaco-editor/editor/editor.api.js";
import editorWorker from "monaco-editor/editor/editor.worker.js?worker&url";
import jsonWorker from "monaco-editor/language/json/json.worker.js?worker&url";
import { useEffect, useRef } from "react";

import type { FileSettings } from "@/lib/file-settings";
import { workerURL } from "@/lib/trusted-types";

// Monaco's workers are our own files (worker-src 'self' in the CSP),
// passed through the app's Trusted Types policy. JSON gets its own, for
// validation and folding; everything else uses the editor's.
self.MonacoEnvironment = {
  getWorker: (_id: string, label: string) =>
    new Worker(workerURL(label === "json" ? jsonWorker : editorWorker), { type: "module" }),
};

// cssColor is a CSS variable as hex, for Monaco (the app's colors are
// oklch, which Monaco can't read): drawn on a canvas and read back. alpha
// (0–1) makes it see-through, for selections and highlights.
function cssColor(variable: string, alpha = 1): string {
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
  const hex = (v: number) => v.toString(16).padStart(2, "0");
  return `#${hex(r)}${hex(g)}${hex(b)}${alpha < 1 ? hex(Math.round(alpha * 255)) : ""}`;
}

// raptorTheme is the editor in the app's colors, all of it: the text and
// gutter, and the menus, suggestions, find box, hovers, and command
// palette Monaco draws itself. Defined again whenever the app's theme
// changes (the html element's dark class).
function raptorTheme(): string {
  const dark = document.documentElement.classList.contains("dark");
  const c = (v: string, a?: number) => cssColor(v, a);
  const card = c("--card");
  const popover = c("--popover");
  const fg = c("--foreground");
  const muted = c("--muted-foreground");
  const border = c("--border");
  const accent = c("--accent");
  const primary = c("--primary");
  const ring = c("--ring");
  monaco.editor.defineTheme("raptor", {
    base: dark ? "vs-dark" : "vs",
    inherit: true,
    rules: [],
    colors: {
      foreground: fg,
      focusBorder: ring,
      "widget.border": border,
      "widget.shadow": "#00000040",
      "selection.background": c("--primary", 0.3),
      // The text.
      "editor.background": card,
      "editor.foreground": fg,
      "editorGutter.background": card,
      "editorStickyScroll.background": card,
      "editorStickyScrollHover.background": accent,
      "minimap.background": card,
      "editorLineNumber.foreground": c("--muted-foreground", 0.6),
      "editorLineNumber.activeForeground": fg,
      "editorCursor.foreground": primary,
      "editor.selectionBackground": c("--primary", 0.28),
      "editor.inactiveSelectionBackground": c("--primary", 0.14),
      "editor.selectionHighlightBackground": c("--primary", 0.14),
      "editor.wordHighlightBackground": c("--primary", 0.12),
      "editor.findMatchBackground": c("--primary", 0.4),
      "editor.findMatchHighlightBackground": c("--primary", 0.2),
      "editor.lineHighlightBackground": c("--accent", 0.35),
      "editor.lineHighlightBorder": "#00000000",
      "editorIndentGuide.background1": c("--border", 0.8),
      "editorIndentGuide.activeBackground1": c("--rule-bright"),
      "editorWhitespace.foreground": c("--muted-foreground", 0.35),
      "editorBracketMatch.background": c("--primary", 0.15),
      "editorBracketMatch.border": c("--primary", 0.6),
      "editorLink.activeForeground": primary,
      "editorError.foreground": c("--destructive"),
      "editorWarning.foreground": c("--warn"),
      "editorOverviewRuler.border": "#00000000",
      "scrollbar.shadow": "#00000000",
      "scrollbarSlider.background": c("--muted-foreground", 0.2),
      "scrollbarSlider.hoverBackground": c("--muted-foreground", 0.32),
      "scrollbarSlider.activeBackground": c("--muted-foreground", 0.44),
      // Widgets: suggestions, hovers, find, and the like.
      "editorWidget.background": popover,
      "editorWidget.foreground": fg,
      "editorWidget.border": border,
      "editorSuggestWidget.background": popover,
      "editorSuggestWidget.border": border,
      "editorSuggestWidget.foreground": fg,
      "editorSuggestWidget.selectedBackground": accent,
      "editorSuggestWidget.selectedForeground": fg,
      "editorSuggestWidget.highlightForeground": primary,
      "editorSuggestWidget.focusHighlightForeground": primary,
      "editorHoverWidget.background": popover,
      "editorHoverWidget.border": border,
      "editorHoverWidget.foreground": fg,
      "editorHoverWidget.statusBarBackground": card,
      "editorMarkerNavigation.background": popover,
      // The right-click menu.
      "menu.background": popover,
      "menu.foreground": fg,
      "menu.border": border,
      "menu.selectionBackground": accent,
      "menu.selectionForeground": fg,
      "menu.separatorBackground": border,
      // The command palette (F1) and other lists.
      "quickInput.background": popover,
      "quickInput.foreground": fg,
      "quickInputTitle.background": card,
      "pickerGroup.foreground": muted,
      "pickerGroup.border": border,
      "list.activeSelectionBackground": accent,
      "list.activeSelectionForeground": fg,
      "list.inactiveSelectionBackground": accent,
      "list.focusBackground": accent,
      "list.focusForeground": fg,
      "list.hoverBackground": c("--accent", 0.6),
      "list.highlightForeground": primary,
      "list.focusOutline": "#00000000",
      "quickInputList.focusBackground": accent,
      "quickInputList.focusForeground": fg,
      "keybindingLabel.background": c("--muted"),
      "keybindingLabel.foreground": fg,
      "keybindingLabel.border": border,
      "keybindingLabel.bottomBorder": border,
      // Text boxes and buttons inside them (find and replace).
      "input.background": card,
      "input.foreground": fg,
      "input.border": border,
      "input.placeholderForeground": muted,
      "inputOption.activeBackground": c("--primary", 0.2),
      "inputOption.activeBorder": primary,
      "inputOption.activeForeground": fg,
      "inputValidation.errorBackground": c("--danger-wash"),
      "inputValidation.errorBorder": c("--destructive"),
      "button.background": primary,
      "button.foreground": c("--primary-foreground"),
      "toolbar.hoverBackground": accent,
      "badge.background": c("--muted"),
      "badge.foreground": fg,
      "progressBar.background": primary,
      descriptionForeground: muted,
      "icon.foreground": muted,
    },
  });
  return "raptor";
}

// useRaptorTheme keeps the editor in the app's colors as they change: the
// account's choice or, for "system", the OS's.
function useRaptorTheme() {
  useEffect(() => {
    monaco.editor.setTheme(raptorTheme());
    const watch = new MutationObserver(() => monaco.editor.setTheme(raptorTheme()));
    watch.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => watch.disconnect();
  }, []);
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
  onRun,
  completions,
}: {
  value: string;
  language: string;
  readOnly: boolean;
  settings: FileSettings;
  onChange: (text: string) => void;
  onSave: () => void;
  // onRun, if set, is Ctrl/Cmd-Enter (the SQL editor runs its query).
  onRun?: () => void;
  // completions are words suggested as you type (a database's tables and
  // columns), with what each is.
  completions?: { label: string; detail: string }[];
}) {
  const box = useRef<HTMLDivElement>(null);
  const editor = useRef<monaco.editor.IStandaloneCodeEditor>(null);
  const handlers = useRef({ onChange, onSave, onRun });
  handlers.current = { onChange, onSave, onRun };
  const words = useRef(completions);
  words.current = completions;
  const settingsAtStart = useRef(settings);
  settingsAtStart.current = settings;
  useRaptorTheme();

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
      fontFamily: getComputedStyle(document.documentElement).getPropertyValue("--font-mono"),
      theme: raptorTheme(),
      scrollBeyondLastLine: false,
      renderLineHighlight: "line",
      tabSize: 2,
      // Later changes are applied below, without a new editor.
      ...options(settingsAtStart.current),
    });
    editor.current = ed;
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => handlers.current.onSave());
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => handlers.current.onRun?.());
    // Suggestions for this editor's model only.
    const provider = monaco.languages.registerCompletionItemProvider(language, {
      provideCompletionItems: (m, position) => {
        if (m !== model || !words.current) return { suggestions: [] };
        const word = m.getWordUntilPosition(position);
        const range = {
          startLineNumber: position.lineNumber,
          endLineNumber: position.lineNumber,
          startColumn: word.startColumn,
          endColumn: word.endColumn,
        };
        return {
          suggestions: words.current.map((w) => ({
            label: w.label,
            detail: w.detail,
            insertText: w.label,
            kind:
              w.detail === "table"
                ? monaco.languages.CompletionItemKind.Struct
                : monaco.languages.CompletionItemKind.Field,
            range,
          })),
        };
      },
    });
    const sub = model.onDidChangeContent(() => handlers.current.onChange(model.getValue()));
    ed.focus();
    return () => {
      provider.dispose();
      sub.dispose();
      ed.dispose();
      model.dispose();
      editor.current = null;
    };
  }, [value, language, readOnly]);

  useEffect(() => {
    editor.current?.updateOptions(options(settings));
  }, [settings]);

  return <div ref={box} className="h-full min-h-0 w-full" />;
}
