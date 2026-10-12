import { create } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { Loader2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { AnnouncementBanner } from "@/components/announcements";
import { useReauth } from "@/components/reauth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  type Announcement,
  AnnouncementSchema,
  StaffService,
} from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";
import { staffClient } from "@/lib/transport";

export const Route = createFileRoute("/admin/announcements")({
  component: AnnouncementsPage,
});

const field =
  "rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

type Draft = {
  id: string;
  level: string;
  title: string;
  body: string;
  link: string;
  starts: string; // datetime-local
  ends: string;
};

const empty: Draft = { id: "", level: "info", title: "", body: "", link: "", starts: "", ends: "" };

// datetime-local wants local time without a zone.
function local(d?: Date): string {
  if (!d) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

function status(a: Announcement): { label: string; variant: "default" | "secondary" | "outline" } {
  const now = Date.now();
  const start = a.startsAt ? timestampDate(a.startsAt).getTime() : 0;
  const end = a.endsAt ? timestampDate(a.endsAt).getTime() : Number.POSITIVE_INFINITY;
  if (now < start) return { label: "Scheduled", variant: "secondary" };
  if (now >= end) return { label: "Ended", variant: "outline" };
  return { label: "Live", variant: "default" };
}

// Announcements: banners everyone signed in sees, from a start to an end.
function AnnouncementsPage() {
  const me = useQuery(StaffService.method.getStaffMe, {});
  const admin = me.data?.role === "admin";
  const list = useQuery(StaffService.method.listAnnouncements, {});
  const client = useQueryClient();
  const withReauth = useReauth();
  const [draft, setDraft] = useState<Draft>(empty);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const preview = create(AnnouncementSchema, {
    level: draft.level,
    title: draft.title || "Title",
    body: draft.body,
    link: draft.link,
  });

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await withReauth(() =>
        staffClient.saveAnnouncement({
          announcement: create(AnnouncementSchema, {
            id: draft.id,
            level: draft.level,
            title: draft.title,
            body: draft.body,
            link: draft.link,
            startsAt: draft.starts ? timestampFromDate(new Date(draft.starts)) : undefined,
            endsAt: draft.ends ? timestampFromDate(new Date(draft.ends)) : undefined,
          }),
        }),
      );
      setDraft(empty);
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid items-start gap-6 lg:grid-cols-5">
      {admin && (
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>{draft.id ? "Edit announcement" : "New announcement"}</CardTitle>
            <CardDescription>
              Shown to everyone signed in, under the header, until it ends or they dismiss it. Plain
              text only.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={save} className="flex flex-col gap-4">
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="an-level">Kind</Label>
                <select
                  id="an-level"
                  className={field}
                  value={draft.level}
                  onChange={(e) => setDraft({ ...draft, level: e.target.value })}
                >
                  <option value="info">News</option>
                  <option value="warning">Warning (an incident)</option>
                  <option value="maintenance">Maintenance</option>
                </select>
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="an-title">Title</Label>
                <Input
                  id="an-title"
                  required
                  maxLength={120}
                  value={draft.title}
                  onChange={(e) => setDraft({ ...draft, title: e.target.value })}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="an-body">Text</Label>
                <textarea
                  id="an-body"
                  rows={3}
                  maxLength={1000}
                  className={field}
                  value={draft.body}
                  onChange={(e) => setDraft({ ...draft, body: e.target.value })}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="an-link">Link (optional)</Label>
                <Input
                  id="an-link"
                  type="url"
                  placeholder="https://status.raptorpanel.net"
                  value={draft.link}
                  onChange={(e) => setDraft({ ...draft, link: e.target.value })}
                />
              </div>
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="an-start">Starts</Label>
                  <input
                    id="an-start"
                    type="datetime-local"
                    className={field}
                    value={draft.starts}
                    onChange={(e) => setDraft({ ...draft, starts: e.target.value })}
                  />
                  <p className="text-xs text-muted-foreground">Empty: now.</p>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="an-end">Ends</Label>
                  <input
                    id="an-end"
                    type="datetime-local"
                    className={field}
                    value={draft.ends}
                    onChange={(e) => setDraft({ ...draft, ends: e.target.value })}
                  />
                  <p className="text-xs text-muted-foreground">Empty: until you end it.</p>
                </div>
              </div>
              <div className="flex flex-col gap-1.5">
                <span className="text-sm font-medium">Preview</span>
                <div className="overflow-hidden rounded-lg border">
                  <AnnouncementBanner a={preview} />
                </div>
              </div>
              {error && <p className="text-sm text-destructive">{error}</p>}
              <div className="flex gap-2">
                <Button type="submit" disabled={busy || !draft.title.trim()}>
                  {busy && <Loader2 className="animate-spin" />}
                  {draft.id ? "Save" : "Publish"}
                </Button>
                {draft.id && (
                  <Button type="button" variant="ghost" onClick={() => setDraft(empty)}>
                    Cancel
                  </Button>
                )}
              </div>
            </form>
          </CardContent>
        </Card>
      )}
      <Card className={admin ? "lg:col-span-3" : "lg:col-span-5"}>
        <CardHeader>
          <CardTitle>Announcements</CardTitle>
          <CardDescription>The newest 100: live, scheduled, and ended.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
          {list.data?.announcements.length === 0 && (
            <p className="text-sm text-muted-foreground">None yet.</p>
          )}
          {list.data?.announcements.map((a) => {
            const st = status(a);
            return (
              <div key={a.id} className="flex flex-col gap-2 rounded-lg border p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="flex items-center gap-2 font-medium">
                    {a.title}
                    <Badge variant={st.variant}>{st.label}</Badge>
                    <Badge variant="outline" className="capitalize">
                      {a.level}
                    </Badge>
                  </span>
                  {admin && st.label !== "Ended" && (
                    <span className="flex gap-1.5">
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() =>
                          setDraft({
                            id: a.id,
                            level: a.level,
                            title: a.title,
                            body: a.body,
                            link: a.link,
                            starts: local(a.startsAt ? timestampDate(a.startsAt) : undefined),
                            ends: local(a.endsAt ? timestampDate(a.endsAt) : undefined),
                          })
                        }
                      >
                        Edit
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="text-destructive"
                        onClick={async () => {
                          try {
                            await withReauth(() => staffClient.endAnnouncement({ id: a.id }));
                            await client.invalidateQueries();
                          } catch (err) {
                            setError(message(err));
                          }
                        }}
                      >
                        End now
                      </Button>
                    </span>
                  )}
                </div>
                {a.body && (
                  <p className="whitespace-pre-line text-sm text-muted-foreground">{a.body}</p>
                )}
                <p className="text-xs text-muted-foreground">
                  From {when(a.startsAt)}
                  {a.endsAt ? ` to ${when(a.endsAt)}` : ", until ended"} · by {a.createdBy}
                </p>
              </div>
            );
          })}
        </CardContent>
      </Card>
    </div>
  );
}
