import { cn } from "cn";
import { Loader2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { Button } from "@/components/ui/button";

// Polar's checkout and its form for adding a card, framed in the app
// instead of on Polar's pages (docs/PANEL.md#billing-polar). This is the
// protocol @polar-sh/checkout (0.4) speaks, without the package: its
// frames post { type, event } messages to the page that embeds them, which
// Polar only lets happen on hosts its Embedding settings list. The frames
// are Polar's origin, so the app's scripts can't read the card, and Polar
// can't touch the app: only messages from that frame and origin count.
//
// The app's CSP has to let Polar's origin be framed (public/_headers).

type Message = { type?: string; event?: string; height?: number };

function dark() {
  return document.documentElement.classList.contains("dark");
}

// listen calls onEvent for each message the frame sends, until stopped.
function listen(
  frame: () => HTMLIFrameElement | null,
  origin: string,
  type: string,
  onEvent: (m: Message) => void,
) {
  const handler = (e: MessageEvent) => {
    const f = frame();
    if (e.origin !== origin || !f || e.source !== f.contentWindow) return;
    const m = e.data as Message;
    if (m && typeof m === "object" && m.type === type && typeof m.event === "string") onEvent(m);
  };
  window.addEventListener("message", handler);
  return () => window.removeEventListener("message", handler);
}

// How long the checkout may take to show before the app sends the owner
// to Polar's own page instead (a host missing from Polar's list never
// loads).
const loadTimeout = 20_000;

// PolarCheckout opens a checkout session's URL over the page. It ends
// with onSuccess once paid (or the card saved for the free month), or
// onClose; onFallback if it never loads.
export function PolarCheckout({
  url,
  origin,
  onSuccess,
  onClose,
  onFallback,
}: {
  url: string;
  origin: string;
  onSuccess: () => void;
  onClose: () => void;
  onFallback: () => void;
}) {
  const ref = useRef<HTMLIFrameElement>(null);
  const [loaded, setLoaded] = useState(false);
  const closable = useRef(true);
  const done = useRef({ onSuccess, onClose, onFallback });
  done.current = { onSuccess, onClose, onFallback };

  // Made once: a new URL would load the frame again.
  const [src] = useState(() => {
    const u = new URL(url);
    u.searchParams.set("embed", "true");
    u.searchParams.set("embed_origin", window.location.origin);
    u.searchParams.set("theme", dark() ? "dark" : "light");
    return u;
  });
  const ok = src.origin === origin;

  useEffect(() => {
    if (!ok) {
      done.current.onFallback();
      return;
    }
    document.body.style.overflow = "hidden";
    let isLoaded = false;
    const timer = setTimeout(() => {
      if (!isLoaded) done.current.onFallback();
    }, loadTimeout);
    const stop = listen(
      () => ref.current,
      origin,
      "POLAR_CHECKOUT",
      (m) => {
        switch (m.event) {
          case "loaded":
            isLoaded = true;
            setLoaded(true);
            break;
          case "confirmed":
            // Paying: it can't be closed until Polar says how it went.
            closable.current = false;
            break;
          case "success":
            closable.current = true;
            done.current.onSuccess();
            break;
          case "close":
            if (closable.current) done.current.onClose();
            break;
        }
      },
    );
    return () => {
      clearTimeout(timer);
      stop();
      document.body.style.overflow = "";
    };
  }, [ok, origin]);

  if (!ok) return null;
  return createPortal(
    <div className="fixed inset-0 z-50 bg-black/50">
      {!loaded && (
        // Until Polar says it's ready, the frame stays hidden (an error
        // page there would cover the app with no way out) and this closes.
        <div className="absolute inset-0 grid place-items-center">
          <div className="flex items-center gap-3 rounded-lg bg-popover px-4 py-3 text-sm text-popover-foreground ring-1 ring-foreground/10">
            <Loader2 className="size-4 animate-spin" />
            Opening checkout…
            <Button size="sm" variant="ghost" onClick={() => done.current.onClose()}>
              Cancel
            </Button>
          </div>
        </div>
      )}
      <iframe
        ref={ref}
        title="Checkout, by Polar"
        src={src.toString()}
        className={cn(
          "absolute inset-0 size-full border-0 bg-transparent",
          !loaded && "pointer-events-none opacity-0",
        )}
        style={{ colorScheme: src.searchParams.get("theme") ?? "light" }}
      />
    </div>,
    document.body,
  );
}

// PolarCardForm is Polar's form for adding a card, in the page. The card
// becomes the default, which is what Polar charges, at once if a payment
// failed.
export function PolarCardForm({
  token,
  origin,
  onSuccess,
  onError,
}: {
  token: string;
  origin: string;
  onSuccess: () => void;
  onError: (code: string) => void;
}) {
  const ref = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(0);
  const done = useRef({ onSuccess, onError });
  done.current = { onSuccess, onError };

  const [src] = useState(() => {
    const u = new URL("/embed/payment-method", origin);
    u.searchParams.set("session_token", token);
    u.searchParams.set("embed", "true");
    u.searchParams.set("embed_origin", window.location.origin);
    u.searchParams.set("mode", "inline");
    u.searchParams.set("theme", dark() ? "dark" : "light");
    return u;
  });

  useEffect(
    () =>
      listen(
        () => ref.current,
        origin,
        "POLAR_PAYMENT_METHOD",
        (m) => {
          switch (m.event) {
            case "resize":
              setHeight(Math.max(0, Math.ceil(Number(m.height) || 0)));
              break;
            case "success":
              done.current.onSuccess();
              break;
            case "error":
              done.current.onError(String((m as { code?: unknown }).code ?? "unknown"));
              break;
          }
        },
      ),
    [origin],
  );

  return (
    <div>
      {height === 0 && (
        <p className="flex items-center gap-2 py-4 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          Loading the card form…
        </p>
      )}
      <iframe
        ref={ref}
        title="Add a card, by Polar"
        src={src.toString()}
        className="block w-full border-0 bg-transparent"
        style={{ height, colorScheme: src.searchParams.get("theme") ?? "light" }}
      />
    </div>
  );
}
