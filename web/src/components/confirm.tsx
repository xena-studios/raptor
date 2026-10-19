import {
  createContext,
  type FormEvent,
  type ReactNode,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
} from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// The app's own confirmations, in place of the browser's confirm() and
// prompt(): a question with a confirm button, optionally with a name to
// type first (for what can't be undone) or a value to enter. useConfirm()
// returns a function that opens one and resolves once it's answered.

export type ConfirmOptions = {
  title: string;
  description?: ReactNode;
  // The confirm button's words; "Confirm" if not given.
  confirm?: string;
  cancel?: string;
  // A red confirm button, for what deletes or replaces.
  destructive?: boolean;
  // The confirm button stays off until this is typed exactly.
  typeToConfirm?: string;
};

export type PromptOptions = Omit<ConfirmOptions, "typeToConfirm"> & {
  label: string;
  initial?: string;
  maxLength?: number;
};

type Api = {
  // true once confirmed, false if cancelled.
  confirm: (o: ConfirmOptions) => Promise<boolean>;
  // What was entered, or null if cancelled.
  prompt: (o: PromptOptions) => Promise<string | null>;
};

const ConfirmContext = createContext<Api | null>(null);

export function useConfirm(): Api {
  const c = useContext(ConfirmContext);
  if (!c) throw new Error("useConfirm needs a ConfirmProvider");
  return c;
}

type Open =
  | { kind: "confirm"; o: ConfirmOptions; resolve: (v: boolean) => void }
  | { kind: "prompt"; o: PromptOptions; resolve: (v: string | null) => void };

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState<Open | null>(null);
  const [value, setValue] = useState("");
  const current = useRef<Open | null>(null);
  current.current = open;

  const confirm = useCallback(
    (o: ConfirmOptions) =>
      new Promise<boolean>((resolve) => {
        setValue("");
        setOpen({ kind: "confirm", o, resolve });
      }),
    [],
  );
  const prompt = useCallback(
    (o: PromptOptions) =>
      new Promise<string | null>((resolve) => {
        setValue(o.initial ?? "");
        setOpen({ kind: "prompt", o, resolve });
      }),
    [],
  );

  const api = useMemo(() => ({ confirm, prompt }), [confirm, prompt]);

  function finish(ok: boolean) {
    const o = current.current;
    if (!o) return;
    if (o.kind === "confirm") o.resolve(ok);
    else o.resolve(ok ? value.trim() : null);
    setOpen(null);
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    if (ready) finish(true);
  }

  const o = open?.o;
  const typed = open?.kind === "confirm" ? open.o.typeToConfirm : undefined;
  const ready =
    open?.kind === "prompt" ? value.trim() !== "" : typed === undefined || value === typed;

  return (
    <ConfirmContext.Provider value={api}>
      {children}
      <Dialog open={open !== null} onOpenChange={(v) => !v && finish(false)}>
        <DialogContent>
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{o?.title}</DialogTitle>
              {o?.description && <DialogDescription>{o.description}</DialogDescription>}
            </DialogHeader>
            {open?.kind === "prompt" && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="confirm-value">{open.o.label}</Label>
                <Input
                  id="confirm-value"
                  autoFocus
                  maxLength={open.o.maxLength}
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                />
              </div>
            )}
            {typed !== undefined && (
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="confirm-value">
                  Type <span className="font-mono normal-case">{typed}</span> to confirm
                </Label>
                <Input
                  id="confirm-value"
                  autoFocus
                  autoComplete="off"
                  spellCheck={false}
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                />
              </div>
            )}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => finish(false)}>
                {o?.cancel ?? "Cancel"}
              </Button>
              <Button
                type="submit"
                variant={o?.destructive ? "destructive" : "default"}
                disabled={!ready}
                autoFocus={open?.kind === "confirm" && typed === undefined}
              >
                {o?.confirm ?? "Confirm"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </ConfirmContext.Provider>
  );
}
