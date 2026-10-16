import { Toast } from "@base-ui/react/toast";
import { cn } from "cn";
import { CircleAlert, CircleCheck, X } from "lucide-react";
import type { ReactNode } from "react";

// Toasts: a short confirmation that something was done (or failed), in the
// corner, gone after a few seconds. The manager is global so any code can
// raise one: toast.success("Moved to the trash").
const manager = Toast.createToastManager();

type Kind = "success" | "error";

function add(type: Kind, title: ReactNode, description?: ReactNode) {
  manager.add({ type, title, description, priority: type === "error" ? "high" : "low" });
}

export const toast = {
  success: (title: ReactNode, description?: ReactNode) => add("success", title, description),
  error: (title: ReactNode, description?: ReactNode) => add("error", title, description),
};

export function ToastProvider({ children }: { children: ReactNode }) {
  return (
    <Toast.Provider toastManager={manager} timeout={4000} limit={4}>
      {children}
      <Toast.Portal>
        <Toast.Viewport className="fixed right-4 bottom-4 z-50 flex w-[min(24rem,calc(100vw-2rem))] flex-col-reverse gap-2 outline-none">
          <Toasts />
        </Toast.Viewport>
      </Toast.Portal>
    </Toast.Provider>
  );
}

function Toasts() {
  const { toasts } = Toast.useToastManager();
  return toasts.map((t) => {
    const Icon = t.type === "error" ? CircleAlert : CircleCheck;
    return (
      <Toast.Root
        key={t.id}
        toast={t}
        className={cn(
          "flex items-start gap-3 rounded-lg border bg-popover p-3 text-sm text-popover-foreground shadow-md outline-none",
          "transition-[opacity,translate] duration-200 ease-out data-ending-style:translate-y-2 data-ending-style:opacity-0 data-starting-style:translate-y-2 data-starting-style:opacity-0 data-limited:hidden motion-reduce:transition-none",
        )}
      >
        <Icon
          className={cn(
            "mt-0.5 size-4 shrink-0",
            t.type === "error" ? "text-destructive" : "text-ok",
          )}
        />
        <Toast.Content className="min-w-0 flex-1">
          <Toast.Title className="font-medium" />
          <Toast.Description className="text-muted-foreground" />
        </Toast.Content>
        <Toast.Close
          aria-label="Dismiss"
          className="-m-1 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <X className="size-3.5" />
        </Toast.Close>
      </Toast.Root>
    );
  });
}
