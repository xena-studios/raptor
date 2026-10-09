import { Link, useRouter } from "@tanstack/react-router";
import { ArrowLeft, Home, RefreshCw, WifiOff } from "lucide-react";
import { type ReactNode, useEffect, useState } from "react";

import { Logo } from "@/components/logo";
import { Button, buttonVariants } from "@/components/ui/button";
import { unreachable } from "@/lib/connection";

// ErrorScreen is a page that went wrong: a big code, a plain sentence, and
// a way on. Never the error's own text: that's for us, not for people.
export function ErrorScreen({
  code,
  title,
  description,
  actions,
  full = true,
}: {
  code?: string;
  title: string;
  description: string;
  actions?: ReactNode;
  // A whole page of its own (outside the app), or a section inside it.
  full?: boolean;
}) {
  return (
    <div
      className={
        full
          ? "flex min-h-screen flex-col items-center justify-center gap-8 px-6 py-12"
          : "flex flex-col items-center justify-center gap-6 px-6 py-20"
      }
    >
      {full && (
        <Link to="/" className="flex items-center gap-2 text-lg font-semibold">
          <Logo className="size-8" /> Raptor
        </Link>
      )}
      <div className="flex max-w-md flex-col items-center gap-3 text-center">
        {code && (
          <p className="font-mono text-6xl font-semibold tracking-tight text-muted-foreground/50">
            {code}
          </p>
        )}
        <h1 className="font-heading text-2xl font-semibold">{title}</h1>
        <p className="text-muted-foreground">{description}</p>
      </div>
      {actions && <div className="flex flex-wrap justify-center gap-2">{actions}</div>}
    </div>
  );
}

function BackButton() {
  return (
    <Button variant="outline" onClick={() => window.history.back()}>
      <ArrowLeft /> Go back
    </Button>
  );
}

function HomeLink() {
  return (
    <Link to="/" className={buttonVariants()}>
      <Home /> Go home
    </Link>
  );
}

export function NotFound({ full = true, what = "page" }: { full?: boolean; what?: string }) {
  return (
    <ErrorScreen
      full={full}
      code="404"
      title={`This ${what} doesn't exist`}
      description={
        what === "page"
          ? "The link may be broken, or the page may have moved."
          : `It may have been deleted, or you may not have access to it. Ask an admin of the org if you think you should.`
      }
      actions={
        <>
          <BackButton />
          <HomeLink />
        </>
      }
    />
  );
}

// Unreachable is the app not reaching the Panel: it tries again on its own
// every 10 seconds.
export function Unreachable({ onRetry }: { onRetry: () => void }) {
  const [seconds, setSeconds] = useState(10);
  useEffect(() => {
    const t = setInterval(() => {
      setSeconds((s) => {
        if (s <= 1) {
          onRetry();
          return 10;
        }
        return s - 1;
      });
    }, 1000);
    return () => clearInterval(t);
  }, [onRetry]);
  return (
    <ErrorScreen
      title="Can't reach Raptor"
      description="Raptor isn't answering right now. Check your internet connection; if it's fine, we're probably already on it. Your servers keep running either way."
      actions={
        <>
          <Button onClick={onRetry}>
            <RefreshCw /> Try again
          </Button>
          <p className="flex w-full items-center justify-center gap-1.5 pt-2 text-xs text-muted-foreground">
            <WifiOff className="size-3.5" /> Trying again in {seconds}s
          </p>
        </>
      }
    />
  );
}

// RouteError is what a page shows when loading it failed.
export function RouteError({ error, reset }: { error: unknown; reset: () => void }) {
  const router = useRouter();
  const retry = () => {
    reset();
    void router.invalidate();
  };
  if (unreachable(error)) return <Unreachable onRetry={retry} />;
  return (
    <ErrorScreen
      title="Something went wrong"
      description="This page hit a problem it couldn't recover from. Trying again usually fixes it; if it keeps happening, let us know what you were doing."
      actions={
        <>
          <Button onClick={retry}>
            <RefreshCw /> Try again
          </Button>
          <HomeLink />
        </>
      }
    />
  );
}
