// The interceptor that raises action toasts; what each says is in
// action-messages.ts (docs/PANEL.md#toasts).
import type { Interceptor } from "@connectrpc/connect";
import { toast } from "@/components/ui/toast";

import { describe } from "@/lib/action-messages";

export const toastActions: Interceptor = (next) => async (req) => {
  const res = await next(req);
  if (!req.stream) {
    const text = describe(
      `${req.service.name}.${req.method.name}`,
      req.message as unknown as Record<string, unknown>,
    );
    if (text) toast.success(text);
  }
  return res;
};
