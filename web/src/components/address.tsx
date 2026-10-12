import { Check, Copy, Eye, EyeOff } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { serverAddress } from "@/lib/addresses";
import { cn } from "@/lib/utils";

// Hidden is an address that points at the machine: dots until revealed, so
// it isn't on screen in a stream or a screenshot. Copying works without
// revealing it.
export function Hidden({
  value,
  label = "address",
  className,
}: {
  value: string;
  label?: string;
  className?: string;
}) {
  const [shown, setShown] = useState(false);
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-0.5", className)}>
      <code className={cn("truncate text-xs", !shown && "tracking-widest text-muted-foreground")}>
        {shown ? value : "••••••••••••"}
      </code>
      <Button
        size="icon-xs"
        variant="ghost"
        aria-label={shown ? `Hide the ${label}` : `Show the ${label}`}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setShown(!shown);
        }}
      >
        {shown ? <EyeOff /> : <Eye />}
      </Button>
      <CopyButton value={value} label={label} />
    </span>
  );
}

function CopyButton({ value, label = "address" }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      size="icon-xs"
      variant="ghost"
      aria-label={`Copy the ${label}`}
      onClick={async (e) => {
        e.preventDefault();
        e.stopPropagation();
        await navigator.clipboard.writeText(value);
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
    >
      {copied ? <Check /> : <Copy />}
    </Button>
  );
}

// ServerAddress is where players connect: the owner's shown address in the
// clear, or the node's own hostname hidden.
export function ServerAddress({
  node,
  port,
  className,
}: {
  node: { shortId: string; displayAddress: string };
  port?: number;
  className?: string;
}) {
  const a = serverAddress(node, port);
  if (a.private) return <Hidden value={a.text} className={className} />;
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-0.5", className)}>
      <code className="truncate text-xs">{a.text}</code>
      <CopyButton value={a.text} />
    </span>
  );
}
