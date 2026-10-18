import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { RaptorMark, Wordmark } from "@/components/logo";
import { SourceLink } from "@/components/source-link";

// The pages outside the app, for signing in: what Raptor is beside the
// form on wide screens, the form alone on narrow ones.

const promises = [
  "Your servers, schedules, and backups keep running if the Panel is offline.",
  "Deleting servers or giving anyone access needs your passkey, and your machine checks it.",
  "Open source under the AGPL: every line that runs your machines is public.",
];

function Brand({ className }: { className?: string }) {
  return (
    <Link to="/" className={className} aria-label="Raptor">
      <RaptorMark className="h-7 w-auto" />
      <Wordmark className="h-3.5 w-auto" />
    </Link>
  );
}

export function AuthLayout({
  title,
  description,
  children,
}: {
  title: string;
  description?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="grid min-h-svh lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
      <aside className="hidden flex-col justify-between border-r bg-sidebar p-10 text-sidebar-foreground lg:flex">
        <Brand className="flex w-fit items-center gap-3" />
        <div className="flex max-w-md flex-col gap-8">
          <p className="font-heading text-2xl leading-snug font-semibold tracking-tight text-balance">
            Game servers on your own machines, run from one place.
          </p>
          <ul className="flex flex-col border-t">
            {promises.map((p) => (
              <li key={p} className="border-b py-3 text-sm text-muted-foreground">
                {p}
              </li>
            ))}
          </ul>
        </div>
        <SourceLink />
      </aside>

      <main className="flex flex-col px-6 py-10 sm:px-10">
        <Brand className="flex w-fit items-center gap-3 lg:hidden" />
        <div className="flex flex-1 items-start justify-center pt-12 pb-10 sm:items-center sm:pt-10">
          <div className="flex w-full max-w-[22rem] flex-col gap-6">
            <header className="flex flex-col gap-1.5">
              <h1 className="font-heading text-2xl font-semibold tracking-tight text-balance">
                {title}
              </h1>
              {description && (
                <p className="text-sm text-pretty text-muted-foreground">{description}</p>
              )}
            </header>
            {children}
          </div>
        </div>
        <div className="flex flex-col items-center gap-1 text-center text-xs text-muted-foreground">
          <p>
            Trouble signing in?{" "}
            <a
              href="https://docs.raptorpanel.net/help/get-help/"
              target="_blank"
              rel="noreferrer"
              className="underline underline-offset-2 hover:text-foreground"
            >
              Get help
            </a>
          </p>
          <SourceLink className="lg:hidden" />
        </div>
      </main>
    </div>
  );
}
