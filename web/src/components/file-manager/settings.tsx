import { Keyboard, Settings } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { type FileSettings, setFileSettings, useFileSettings } from "@/lib/file-settings";

const toggles: { key: keyof FileSettings; title: string; description: string }[] = [
  {
    key: "singleClick",
    title: "Single-click open",
    description: "One click opens; Ctrl or Shift-click selects. Off: double-click to open.",
  },
  { key: "wordWrap", title: "Word wrap", description: "Wrap long lines in the editor." },
  {
    key: "stickyScroll",
    title: "Sticky scroll",
    description: "Keep the current section's heading at the top as you scroll.",
  },
  {
    key: "hideIndentGuides",
    title: "Hide indent guides",
    description: "Hide the vertical indentation lines.",
  },
  {
    key: "hideAutocomplete",
    title: "Hide autocomplete",
    description: "Stop suggestions from popping up as you type.",
  },
  {
    key: "autoSave",
    title: "Auto-save",
    description:
      "Save the open file to your server a few seconds after you stop typing. Unsaved work is kept in this browser either way.",
  },
];

export function SettingsMenu({ onShortcuts }: { onShortcuts: () => void }) {
  const s = useFileSettings();
  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="File manager settings"
            title="Settings"
          />
        }
      >
        <Settings />
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 p-0">
        <p className="border-b px-4 py-3 text-sm font-medium">File manager settings</p>
        <ul className="divide-y">
          {toggles.map((t) => (
            <li key={t.key} className="flex items-start gap-4 px-4 py-3">
              <label htmlFor={`fs-${t.key}`} className="min-w-0 flex-1">
                <span className="block text-sm font-medium">{t.title}</span>
                <span className="block text-xs text-muted-foreground">{t.description}</span>
              </label>
              <Switch
                id={`fs-${t.key}`}
                checked={s[t.key] as boolean}
                onCheckedChange={(v) => setFileSettings({ [t.key]: v })}
              />
            </li>
          ))}
        </ul>
        <div className="border-t p-1.5">
          <Button variant="ghost" size="sm" className="w-full justify-start" onClick={onShortcuts}>
            <Keyboard /> Keyboard shortcuts
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
