import { useEffect, useImperativeHandle, useRef } from "react";

// Cloudflare Turnstile, through the verify page on its own origin
// (web/verify, docs/DECISIONS.md #192): Turnstile's script never runs on
// the app's origin, which is the one passkeys belong to. The iframe gets
// no permissions (no `allow`), so it can't ask for passkeys either.
export const turnstileURL: string | undefined = import.meta.env.VITE_TURNSTILE_URL;
export const turnstileSitekey: string | undefined = import.meta.env.VITE_TURNSTILE_SITEKEY;
export const turnstileOn = !!(turnstileURL && turnstileSitekey);

export type TurnstileHandle = { reset: () => void };

export function Turnstile({
  onToken,
  ref,
}: {
  onToken: (token: string) => void;
  ref?: React.Ref<TurnstileHandle>;
}) {
  const frame = useRef<HTMLIFrameElement>(null);
  const origin = turnstileURL ? new URL(turnstileURL).origin : "";

  useEffect(() => {
    function onMessage(e: MessageEvent) {
      if (e.origin !== origin || e.source !== frame.current?.contentWindow) return;
      if (e.data?.type === "turnstile" && typeof e.data.token === "string") onToken(e.data.token);
    }
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [origin, onToken]);

  useImperativeHandle(ref, () => ({
    reset: () => {
      onToken("");
      frame.current?.contentWindow?.postMessage({ type: "turnstile.reset" }, origin);
    },
  }));

  if (!turnstileURL || !turnstileSitekey) return null;
  const src = new URL(turnstileURL);
  src.searchParams.set("sitekey", turnstileSitekey);
  src.searchParams.set("origin", window.location.origin);
  return (
    <iframe
      ref={frame}
      src={src.toString()}
      title="Security check"
      className="h-[72px] w-full border-0"
      sandbox="allow-scripts allow-same-origin allow-popups"
      referrerPolicy="no-referrer"
    />
  );
}
