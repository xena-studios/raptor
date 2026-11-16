import { Link } from "@tanstack/react-router";
import { CircleAlert, X } from "lucide-react";
import type { ReactNode } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import type { HintTab, Problem } from "@/lib/problems";

// ProblemHint says what a known console error means and where the fix is
// (#430): one sentence, one button, and the line it came from for whoever
// wants to see why.
export function ProblemHint({
  problem,
  orgId,
  serverId,
  onDismiss,
}: {
  problem: Extract<Problem, { kind: "hint" }>;
  orgId: string;
  serverId: string;
  onDismiss: () => void;
}) {
  const a = problem.action;
  return (
    <Alert>
      <CircleAlert />
      <AlertTitle className="flex items-start justify-between gap-2">
        {problem.title}
        <Button
          size="icon-xs"
          variant="ghost"
          aria-label="Dismiss"
          className="-mt-1 -mr-1 text-muted-foreground"
          onClick={onDismiss}
        >
          <X />
        </Button>
      </AlertTitle>
      <AlertDescription className="flex flex-col gap-2">
        <p>{problem.text}</p>
        <details className="text-xs">
          <summary className="cursor-pointer text-muted-foreground">The console line</summary>
          <code className="mt-1 block break-all rounded bg-muted px-2 py-1">{problem.line}</code>
        </details>
        {a && (
          <TabLink tab={a.tab} orgId={orgId} serverId={serverId}>
            {a.label}
          </TabLink>
        )}
      </AlertDescription>
    </Alert>
  );
}

function TabLink({
  tab,
  orgId,
  serverId,
  children,
}: {
  tab: HintTab;
  orgId: string;
  serverId: string;
  children: ReactNode;
}) {
  const className = buttonVariants({ size: "sm", variant: "outline", className: "self-start" });
  const params = { orgId, serverId };
  switch (tab) {
    case "addons/mods":
    case "addons/plugins":
      return (
        <Link
          to="/orgs/$orgId/servers/$serverId/addons/$kind"
          params={{ ...params, kind: tab.slice("addons/".length) }}
          className={className}
        >
          {children}
        </Link>
      );
    case "network":
      return (
        <Link to="/orgs/$orgId/servers/$serverId/network" params={params} className={className}>
          {children}
        </Link>
      );
    case "files":
      return (
        <Link to="/orgs/$orgId/servers/$serverId/files" params={params} className={className}>
          {children}
        </Link>
      );
    case "settings":
      return (
        <Link to="/orgs/$orgId/servers/$serverId/settings" params={params} className={className}>
          {children}
        </Link>
      );
  }
}
