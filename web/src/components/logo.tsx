// Raptor's marks (docs/DESIGN.md#7-brand). The site's header is a copy of these,
// and the favicons (web/public/ and site/public/favicon.svg) put the same claw
// R on a red tile: change them together.

import { clawPath as claw, raptorPath, wordmarkPath } from "@/lib/brand";

// Logo is the claw R on its own, in red: the small mark, where there's no room
// for the raptor and the wordmark (the collapsed sidebar).
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <path
        d={claw}
        transform="translate(-0.9 28.3) scale(0.0335)"
        fillRule="evenodd"
        className="fill-primary"
      />
    </svg>
  );
}

// RaptorMark is the running raptor, which goes beside the wordmark in the
// full logo. It's wide, so it's never used alone at small sizes.
export function RaptorMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 3 96 46" className={className} aria-hidden="true">
      <path d={raptorPath} fillRule="evenodd" className="fill-primary" />
    </svg>
  );
}

// Wordmark is "RAPTOR" in Archivo ExtraBold at width 118 with 0.06em
// tracking, outlined so no font is loaded for it.
export function Wordmark({ className }: { className?: string }) {
  return (
    <svg viewBox="82 -699 5356 711" className={className} role="img" aria-label="Raptor">
      <path d={wordmarkPath} fill="currentColor" />
    </svg>
  );
}
