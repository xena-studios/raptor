// Logo is Raptor's mark. A placeholder until the real logo is in the repo:
// swap the SVG here and every page follows.
export function Logo({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" className={className} aria-hidden="true">
      <rect width="32" height="32" rx="8" className="fill-primary" />
      <path
        d="M9 23V9h7.5a4.5 4.5 0 0 1 1.2 8.84L23 23h-4l-4.6-5H13v5H9Zm4-8.5h3.3a1.75 1.75 0 0 0 0-3.5H13v3.5Z"
        className="fill-primary-foreground"
      />
    </svg>
  );
}
