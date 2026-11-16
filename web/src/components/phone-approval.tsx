import { Code, ConnectError } from "@connectrpc/connect";
import { Loader2, Smartphone } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { message } from "@/lib/errors";
import { authClient } from "@/lib/transport";

// PhoneApproval asks the person's phone to approve (docs/PANEL.md#phone-
// approvals): Raptor's app shows a notification, and they pick the number
// shown here. It waits for the answer and calls onApproved with the
// approval to finish with. Accounts without a phone that approves get
// onUnavailable, and the page offers codes alone.

type State =
  | { step: "starting" }
  | { step: "waiting"; id: string; number: number; notified: number }
  | { step: "done" }
  | { step: "failed"; text: string };

export function PhoneApproval({
  purpose,
  onApproved,
  onUnavailable,
}: {
  purpose: "signin" | "reauth";
  onApproved: (approvalId: string) => Promise<void>;
  onUnavailable?: () => void;
}) {
  const [state, setState] = useState<State>({ step: "starting" });
  const run = useRef(0);

  const start = useCallback(async () => {
    const mine = ++run.current;
    setState({ step: "starting" });
    try {
      const res = await authClient.startPhoneApproval({ purpose });
      if (run.current !== mine) return;
      setState({ step: "waiting", id: res.approvalId, number: res.number, notified: res.notified });
    } catch (err) {
      if (err instanceof ConnectError && err.code === Code.FailedPrecondition) {
        onUnavailable?.();
        return;
      }
      setState({ step: "failed", text: message(err) });
    }
  }, [purpose, onUnavailable]);

  useEffect(() => {
    start();
    return () => {
      run.current++;
    };
  }, [start]);

  // Wait for the phone: a look every 2 seconds.
  useEffect(() => {
    if (state.step !== "waiting") return;
    let stop = false;
    const id = state.id;
    const timer = setInterval(async () => {
      try {
        const { status } = await authClient.checkPhoneApproval({ approvalId: id });
        if (stop) return;
        if (status === "approved") {
          stop = true;
          setState({ step: "done" });
          await onApproved(id);
        } else if (status === "denied") {
          stop = true;
          setState({ step: "failed", text: "Your phone denied it." });
        } else if (status === "expired") {
          stop = true;
          setState({ step: "failed", text: "Your phone didn't answer in time." });
        }
      } catch (err) {
        stop = true;
        setState({ step: "failed", text: message(err) });
      }
    }, 2000);
    return () => {
      stop = true;
      clearInterval(timer);
    };
  }, [state, onApproved]);

  return (
    <div className="flex flex-col items-center gap-3 rounded-xl bg-card px-4 py-5 text-center ring-1 ring-foreground/10">
      <Smartphone className="size-5 text-muted-foreground" aria-hidden />
      {state.step === "waiting" ? (
        <>
          <p className="text-sm">
            {state.notified > 0
              ? "Open the notification on your phone and pick"
              : "Open Raptor on your phone and pick"}
          </p>
          <p
            className="font-mono text-4xl font-medium tracking-wider tabular-nums"
            aria-live="polite"
          >
            {state.number}
          </p>
          <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Loader2 className="size-3 animate-spin" aria-hidden /> Waiting for your phone
          </p>
        </>
      ) : state.step === "failed" ? (
        <>
          <p className="text-sm text-destructive" role="alert">
            {state.text}
          </p>
          <Button variant="outline" size="sm" onClick={start}>
            Send again
          </Button>
        </>
      ) : (
        <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden />
          {state.step === "done" ? "Approved" : "Asking your phone"}
        </p>
      )}
    </div>
  );
}
