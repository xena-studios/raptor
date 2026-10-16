import type { ReactNode } from "react";

import { RaptorMark } from "@/components/logo";

// PopoutHeader is a pop-out window's one bar: Raptor's mark, what's in the
// window, a way back to the full app, and its actions.
export function PopoutHeader({
  title,
  back,
  actions,
  error,
}: {
  title: ReactNode;
  back: ReactNode;
  actions?: ReactNode;
  error?: string;
}) {
  return (
    <header className="shrink-0 border-b">
      <div className="flex min-h-12 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2">
        <RaptorMark className="size-5 shrink-0 text-primary" />
        <h1 className="flex min-w-0 flex-1 items-center gap-2 text-sm font-medium">{title}</h1>
        <span className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">
          {back}
        </span>
        {actions}
      </div>
      {error && <p className="px-4 pb-2 text-sm text-destructive">{error}</p>}
    </header>
  );
}
