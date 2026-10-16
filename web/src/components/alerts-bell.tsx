import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { Link } from "@tanstack/react-router";
import { cn } from "cn";
import { Bell, BellOff, Settings2, X } from "lucide-react";
import { useState } from "react";

import { MyChannels } from "@/components/org/alerts";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { AlertService, type Notification } from "@/gen/raptor/panel/v1/alert_pb";
import { message } from "@/lib/errors";
import { ago } from "@/lib/format";
import { shortId } from "@/lib/short-id";

// AlertsBell is the inbox at the top of every page: the alerts sent to the
// user, from all their orgs, to read and dismiss; and, behind its settings
// button, how they want to be told besides (docs/PANEL.md#alerts).
export function AlertsBell() {
  const list = useQuery(AlertService.method.listNotifications, {}, { refetchInterval: 30_000 });
  const see = useMutation(AlertService.method.seeNotifications);
  const dismiss = useMutation(AlertService.method.dismissNotifications);
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState(false);
  // What was new when it was opened stays marked while it's open.
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  const [error, setError] = useState("");
  const items = list.data?.notifications ?? [];
  const unseen = list.data?.unseen ?? 0;

  async function refresh() {
    await list.refetch();
  }

  async function clear(ids: string[]) {
    setError("");
    try {
      await dismiss.mutateAsync({ ids });
      await refresh();
    } catch (err) {
      setError(message(err));
    }
  }

  function toggle(o: boolean) {
    setOpen(o);
    if (!o) return;
    setFresh(new Set(items.filter((n) => !n.seen).map((n) => n.id)));
    if (unseen > 0) void see.mutateAsync({}).then(refresh);
  }

  return (
    <>
      <Popover open={open} onOpenChange={toggle}>
        <PopoverTrigger
          render={
            <Button
              variant="ghost"
              size="icon-sm"
              className="relative"
              aria-label={unseen ? `Alerts, ${unseen} new` : "Alerts"}
            />
          }
        >
          <Bell />
          {unseen > 0 && (
            <span className="absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 font-mono text-[10px] leading-none font-medium text-primary-foreground tabular-nums">
              {unseen > 99 ? "99+" : unseen}
            </span>
          )}
        </PopoverTrigger>
        <PopoverContent align="end" className="w-96 gap-0 p-0">
          <div className="flex h-11 items-center gap-1 border-b pr-1.5 pl-3">
            <span className="flex-1 text-sm font-medium">Alerts</span>
            {items.length > 0 && (
              <Button
                size="xs"
                variant="ghost"
                disabled={dismiss.isPending}
                onClick={() => void clear([])}
              >
                Dismiss all
              </Button>
            )}
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label="How you're told"
              title="How you're told"
              onClick={() => {
                setOpen(false);
                setSettings(true);
              }}
            >
              <Settings2 />
            </Button>
          </div>
          {error && <p className="border-b px-3 py-2 text-xs text-destructive">{error}</p>}
          {list.error && (
            <p className="px-3 py-6 text-center text-sm text-destructive">{message(list.error)}</p>
          )}
          {list.data && items.length === 0 && (
            <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
              <BellOff className="size-5 text-muted-foreground" />
              <p className="text-sm font-medium">Nothing new</p>
              <p className="text-xs text-muted-foreground">
                Alerts your orgs send you show up here, and in the ways you've picked under
                settings.
              </p>
            </div>
          )}
          {items.length > 0 && (
            <ul className="max-h-[28rem] divide-y overflow-auto">
              {items.map((n) => (
                <Item
                  key={n.id}
                  n={n}
                  fresh={fresh.has(n.id)}
                  onOpen={() => setOpen(false)}
                  onDismiss={() => void clear([n.id])}
                />
              ))}
            </ul>
          )}
        </PopoverContent>
      </Popover>

      <Dialog open={settings} onOpenChange={setSettings}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>How you're told</DialogTitle>
            <DialogDescription>
              Alerts sent to you are always here, under the bell. Add the other ways you want them
              too. Which alerts you get is up to each org's admins, in its Alerts settings.
            </DialogDescription>
          </DialogHeader>
          <MyChannels />
        </DialogContent>
      </Dialog>
    </>
  );
}

const levelDot: Record<string, string> = {
  critical: "bg-destructive",
  warning: "bg-warn",
  info: "bg-muted-foreground",
};

// Item is one alert: what happened, where, and when; it opens the page
// it's about.
function Item({
  n,
  fresh,
  onOpen,
  onDismiss,
}: {
  n: Notification;
  fresh: boolean;
  onOpen: () => void;
  onDismiss: () => void;
}) {
  const where = [n.serverName, n.nodeName].filter(Boolean).join(" on ");
  const body = (
    <>
      <span className="flex items-baseline gap-2">
        <span
          aria-hidden
          className={cn(
            "size-2 shrink-0 translate-y-[-1px] rounded-full",
            n.resolved ? "bg-ok" : (levelDot[n.level] ?? levelDot.info),
          )}
        />
        <span className="min-w-0 flex-1 truncate font-medium">{n.title}</span>
        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
          {n.at ? ago(timestampDate(n.at)) : ""}
        </span>
      </span>
      <span className="truncate pl-4 text-xs text-muted-foreground">
        {[where, n.orgName].filter(Boolean).join(" · ")}
      </span>
      {n.text && <span className="line-clamp-2 pl-4 text-xs text-muted-foreground">{n.text}</span>}
    </>
  );
  const cls = "flex min-w-0 flex-1 flex-col gap-0.5 py-2.5 pr-1 pl-3 text-left text-sm";
  return (
    <li className={cn("group flex items-start hover:bg-muted/50", fresh && "bg-brand-wash/40")}>
      {n.serverId && n.orgId ? (
        <Link
          to="/orgs/$orgId/servers/$serverId"
          params={{ orgId: shortId(n.orgId), serverId: shortId(n.serverId) }}
          className={cls}
          onClick={onOpen}
        >
          {body}
        </Link>
      ) : n.nodeId && n.orgId ? (
        <Link
          to="/orgs/$orgId/nodes/$nodeId"
          params={{ orgId: shortId(n.orgId), nodeId: shortId(n.nodeId) }}
          className={cls}
          onClick={onOpen}
        >
          {body}
        </Link>
      ) : (
        <div className={cls}>{body}</div>
      )}
      <Button
        size="icon-xs"
        variant="ghost"
        className="mt-2 mr-1.5 text-muted-foreground"
        aria-label={`Dismiss "${n.title}"`}
        onClick={onDismiss}
      >
        <X />
      </Button>
    </li>
  );
}
