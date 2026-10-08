import { File, FileArchive, FileCode, FileText, Folder, Link2, Lock } from "lucide-react";

import { isArchive, languageFor } from "@/lib/files";
import { type Entry, kind } from "./data";

// EntryIcon is a file or folder's icon: folders blue, config and code
// files purple, archives amber.
export function EntryIcon({ e }: { e: Entry }) {
  const cls = "size-4 shrink-0";
  if (e.denied) return <Lock className={`${cls} text-muted-foreground`} />;
  if (kind(e) === "dir") return <Folder className={`${cls} text-sky-500`} />;
  if (e.type === "symlink") return <Link2 className={`${cls} text-muted-foreground`} />;
  if (isArchive(e.name)) return <FileArchive className={`${cls} text-amber-500`} />;
  const lang = languageFor(e.name);
  if (lang === "plaintext") {
    return /\.(txt|log|md)$/i.test(e.name) ? (
      <FileText className={`${cls} text-muted-foreground`} />
    ) : (
      <File className={`${cls} text-muted-foreground`} />
    );
  }
  return <FileCode className={`${cls} text-violet-500`} />;
}
