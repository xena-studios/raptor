import { json } from "@codemirror/lang-json";
import { xml } from "@codemirror/lang-xml";
import { yaml } from "@codemirror/lang-yaml";
import { StreamLanguage } from "@codemirror/language";
import { properties } from "@codemirror/legacy-modes/mode/properties";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { toml } from "@codemirror/legacy-modes/mode/toml";
import { EditorState, type Extension } from "@codemirror/state";
import { oneDark } from "@codemirror/theme-one-dark";
import { keymap } from "@codemirror/view";
import { basicSetup, EditorView } from "codemirror";
import { useEffect, useRef } from "react";

import type { Language } from "@/lib/files";

function language(l: Language): Extension {
  switch (l) {
    case "json":
      return json();
    case "yaml":
      return yaml();
    case "xml":
      return xml();
    case "properties":
    case "ini":
      return StreamLanguage.define(properties);
    case "toml":
      return StreamLanguage.define(toml);
    case "shell":
      return StreamLanguage.define(shell);
  }
  return [];
}

// Editor edits a text file (CodeMirror: no workers and no eval, so it runs
// under the app's CSP). Loaded lazily with the file manager's editor.
// Ctrl/Cmd-S saves.
export default function Editor({
  initial,
  lang,
  readOnly,
  onChange,
  onSave,
}: {
  initial: string;
  lang: Language;
  readOnly: boolean;
  onChange: (text: string) => void;
  onSave: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const handlers = useRef({ onChange, onSave });
  handlers.current = { onChange, onSave };

  useEffect(() => {
    if (!box.current) return;
    const view = new EditorView({
      parent: box.current,
      state: EditorState.create({
        doc: initial,
        extensions: [
          basicSetup,
          oneDark,
          language(lang),
          EditorState.readOnly.of(readOnly),
          keymap.of([
            {
              key: "Mod-s",
              preventDefault: true,
              run: () => {
                handlers.current.onSave();
                return true;
              },
            },
          ]),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) handlers.current.onChange(u.state.doc.toString());
          }),
          EditorView.theme({ "&": { height: "32rem" }, ".cm-scroller": { overflow: "auto" } }),
        ],
      }),
    });
    return () => view.destroy();
  }, [initial, lang, readOnly]);

  return <div ref={box} className="overflow-hidden rounded-lg border" />;
}
