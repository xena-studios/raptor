import { useQuery } from "@connectrpc/connect-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ChevronDown, Download, FileUp, Pencil, Plus, Rocket, SquarePen } from "lucide-react";
import { useState } from "react";

import { EggImport } from "@/components/egg-import";
import { NotFound } from "@/components/error-screen";
import { PageHeader, tabClass } from "@/components/page";
import { Button, buttonVariants } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { EggService } from "@/gen/raptor/panel/v1/egg_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { blankState, categoryName, download, originNames } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { isAdmin } from "@/lib/format";

import { AddEggButton, CommunityList, EggReports, ModerationCard, ReportButton } from "./community";
import { CustomizeButton, EggDetails } from "./detail";
import { EggEditor } from "./editor";
import { EggBadges, EggIcon, EggList, type Library } from "./library";
import { EggManagement } from "./manage";

// useCanChange reports whether the caller changes this library's eggs: an
// org's admins and owners, or staff admins for the official one.
export function useCanChange(lib: Library): boolean {
  const orgs = useQuery(OrgService.method.listOrgs, {}, { enabled: !!lib.orgId });
  const staff = useQuery(StaffService.method.getStaffMe, {}, { enabled: !lib.orgId });
  if (lib.orgId) return isAdmin(orgs.data?.orgs.find((o) => o.id === lib.orgId)?.role);
  return staff.data?.role === "admin";
}

function NewEggMenu({ lib }: { lib: Library }) {
  const navigate = useNavigate();
  const [importing, setImporting] = useState(false);
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger render={<Button size="sm" />}>
          <Plus /> New egg <ChevronDown />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem onClick={() => navigate(lib.create)}>
            <SquarePen /> Make from scratch
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => setImporting(true)}>
            <FileUp /> Import an egg
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <EggImport
        orgId={lib.orgId}
        open={importing}
        onOpenChange={setImporting}
        onImported={(id) => navigate(lib.detail(id))}
      />
    </>
  );
}

// LibraryPage lists the library's eggs, the community's, and (for
// staff) reports.
export function LibraryPage({ lib }: { lib: Library }) {
  const canChange = useCanChange(lib);
  type Tab = "library" | "community" | "reports";
  const [tab, setTab] = useState<Tab>("library");
  const list = useQuery(EggService.method.listEggs, { orgId: lib.orgId });
  const action = canChange && <NewEggMenu lib={lib} />;
  const tabs: [Tab, string][] = [
    ["library", lib.orgId ? "Your library" : "Official"],
    ["community", "Community"],
    ...(lib.orgId ? [] : [["reports", "Reports"] as [Tab, string]]),
  ];
  return (
    <>
      <PageHeader
        title={lib.orgId ? "Eggs" : "Official eggs"}
        description={
          lib.orgId
            ? "What servers are made from: your own eggs, and Raptor's. Certified ones are tested with every release."
            : "Raptor's own eggs, offered to every org beside the built-in ones."
        }
        actions={action}
      />
      <nav aria-label="Egg lists" className="-mb-2 flex gap-4 border-b">
        {tabs.map(([key, label]) => (
          <button
            key={key}
            type="button"
            className={tabClass}
            data-status={tab === key ? "active" : undefined}
            aria-current={tab === key ? "page" : undefined}
            onClick={() => setTab(key)}
          >
            {label}
          </button>
        ))}
      </nav>
      {tab === "library" && (
        <>
          {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
          <EggList
            lib={lib}
            eggs={list.data?.eggs ?? []}
            loading={list.isPending}
            action={action}
          />
        </>
      )}
      {tab === "community" && <CommunityList lib={lib} />}
      {tab === "reports" && <EggReports />}
    </>
  );
}

// EggPage is one egg: what it runs, and what can be done with it.
export function EggPage({ lib, id }: { lib: Library; id: string }) {
  const canChange = useCanChange(lib);
  const d = useQuery(EggService.method.getEggDetails, { orgId: lib.orgId, id });
  const egg = d.data?.egg;
  if (d.error) {
    return <NotFound full={false} what="egg" />;
  }
  if (!egg || !d.data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const mine = d.data.canEdit;
  const community = egg.scope === "community";
  return (
    <>
      <PageHeader
        back={{ label: lib.orgId ? "Eggs" : "Official eggs", ...lib.list }}
        title={
          <span className="flex flex-wrap items-center gap-2.5">
            <EggIcon category={egg.category} />
            {egg.name}
            <EggBadges egg={egg} showOfficial={!!lib.orgId} />
          </span>
        }
        description={[
          categoryName(egg.category),
          egg.version > 0 ? `v${egg.version}` : "",
          originNames[egg.origin],
          egg.parentName ? `based on ${egg.parentName}` : "",
          egg.publisher ? `shared by ${egg.publisher}` : "",
        ]
          .filter(Boolean)
          .join(" · ")}
        actions={
          <>
            <Button variant="outline" onClick={() => download(egg.name, d.data.file)}>
              <Download /> Export
            </Button>
            {mine && (
              <Link {...lib.edit(egg.id)} className={buttonVariants({ variant: "outline" })}>
                <Pencil /> Edit
              </Link>
            )}
            {community && lib.orgId && <ReportButton lib={lib} id={egg.id} />}
            {canChange && (
              <CustomizeButton
                lib={lib}
                id={egg.id}
                label={
                  mine
                    ? "Duplicate"
                    : community && !lib.orgId
                      ? "Make an official copy"
                      : "Customize"
                }
              />
            )}
            {community && lib.orgId && canChange && <AddEggButton lib={lib} d={d.data} />}
            {lib.orgId && !community && canChange && egg.status === "published" && (
              <Link
                to="/orgs/$orgId/servers/new"
                params={{ orgId: lib.orgId }}
                search={{ egg: egg.id }}
                className={buttonVariants()}
              >
                <Rocket /> Use egg
              </Link>
            )}
          </>
        }
      />
      {community && !lib.orgId && <ModerationCard d={d.data} />}
      <EggDetails d={d.data} />
    </>
  );
}

// NewEggPage makes an egg from scratch.
export function NewEggPage({ lib }: { lib: Library }) {
  return (
    <>
      <PageHeader
        back={{ label: lib.orgId ? "Eggs" : "Official eggs", ...lib.list }}
        title="New egg"
        description="Saved as a draft; publish it when it's ready."
      />
      <EggEditor lib={lib} initial={blankState()} />
    </>
  );
}

// EditEggPage edits an egg, and publishes, archives, or deletes it.
export function EditEggPage({ lib, id }: { lib: Library; id: string }) {
  const d = useQuery(EggService.method.getEggDetails, { orgId: lib.orgId, id });
  const egg = d.data?.egg;
  const spec = d.data?.spec;
  if (d.error || (d.data && !d.data.canEdit)) {
    return <NotFound full={false} what="egg" />;
  }
  if (!egg || !spec || !d.data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return (
    <>
      <PageHeader
        back={{ label: egg.name, ...lib.detail(egg.id) }}
        title={`Edit ${egg.name}`}
        description={`Version ${egg.version}. Saving makes a new version; servers made before keep theirs.`}
      />
      <EggEditor
        lib={lib}
        eggId={egg.id}
        version={egg.version}
        file={d.data.file}
        initial={{ spec, summary: egg.summary, category: egg.category }}
      />
      <EggManagement lib={lib} d={d.data} />
    </>
  );
}
