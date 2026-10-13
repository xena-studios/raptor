import { useQuery } from "@connectrpc/connect-query";
import { ExternalLink, Info, TriangleAlert, Wrench, X } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { type Announcement, StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { cn } from "@/lib/utils";

// Raptor's announcements (docs/PANEL.md#announcements), as banners under
// the header. Their text is plain text, never HTML. Dismissing one hides
// it in this browser.

const dismissedKey = "raptor.announcements.dismissed";

function dismissed(): string[] {
  try {
    return JSON.parse(localStorage.getItem(dismissedKey) ?? "[]") as string[];
  } catch {
    return [];
  }
}

export const levelStyles = {
  info: { icon: Info, className: "border-[var(--series-1)]/30 bg-[var(--series-1)]/10" },
  warning: { icon: TriangleAlert, className: "border-warn/40 bg-warn-wash" },
  maintenance: { icon: Wrench, className: "border-violet-500/40 bg-violet-500/10" },
} as const;

export function AnnouncementBanner({ a, onDismiss }: { a: Announcement; onDismiss?: () => void }) {
  const style = levelStyles[a.level as keyof typeof levelStyles] ?? levelStyles.info;
  const Icon = style.icon;
  return (
    <div className={cn("flex items-start gap-3 border-b px-4 py-2.5 text-sm", style.className)}>
      <Icon className="mt-0.5 size-4 shrink-0" />
      <div className="min-w-0 flex-1">
        <p className="font-medium">{a.title}</p>
        {a.body && <p className="whitespace-pre-line text-muted-foreground">{a.body}</p>}
        {a.link && (
          <a
            href={a.link}
            target="_blank"
            rel="noopener noreferrer"
            className="mt-0.5 inline-flex items-center gap-1 underline underline-offset-2"
          >
            More <ExternalLink className="size-3" />
          </a>
        )}
      </div>
      {onDismiss && (
        <Button size="icon-xs" variant="ghost" aria-label="Dismiss" onClick={onDismiss}>
          <X />
        </Button>
      )}
    </div>
  );
}

export function Announcements() {
  const live = useQuery(
    StaffService.method.listActiveAnnouncements,
    {},
    { refetchInterval: 5 * 60_000 },
  );
  const [hidden, setHidden] = useState<string[]>(dismissed);
  const shown = (live.data?.announcements ?? []).filter((a) => !hidden.includes(a.id));
  if (shown.length === 0) return null;
  return (
    <section aria-label="Announcements">
      {shown.map((a) => (
        <AnnouncementBanner
          key={a.id}
          a={a}
          onDismiss={() => {
            const next = [...hidden, a.id].slice(-50);
            setHidden(next);
            try {
              localStorage.setItem(dismissedKey, JSON.stringify(next));
            } catch {
              // Private browsing: it's hidden until the page reloads.
            }
          }}
        />
      ))}
    </section>
  );
}
