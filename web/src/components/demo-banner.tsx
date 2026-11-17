import { SandboxBar } from "@/components/demo-sandbox";
import { demoBanner, getRaptorURL } from "@/lib/demo";

// DemoBanner is on every page of the public demo (#449): what it is, and
// where to get Raptor for real. In the demo org it also shows the caller's
// sandbox: its place in the queue, or the time it has left.
export function DemoBanner({ orgId }: { orgId?: string }) {
  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-primary/30 bg-primary/5 px-6 py-2 text-sm"
    >
      <span>{demoBanner}</span>
      <a
        href={getRaptorURL}
        target="_blank"
        rel="noopener"
        className="font-medium text-primary underline underline-offset-3"
      >
        Get Raptor
      </a>
      {orgId && (
        <span className="ml-auto">
          <SandboxBar orgId={orgId} />
        </span>
      )}
    </div>
  );
}
