import { ExternalLink } from "lucide-react";
import type { ReactNode } from "react";

import { docs } from "@/lib/help";

// HelpLink is a link to a docs page, opened in a new tab, for where
// people get stuck.
export function HelpLink({ path, children }: { path: string; children: ReactNode }) {
  return (
    <a
      href={docs(path)}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1 text-foreground underline underline-offset-2 hover:text-primary"
    >
      {children}
      <ExternalLink className="size-3" aria-hidden />
      <span className="sr-only">(opens the docs in a new tab)</span>
    </a>
  );
}
