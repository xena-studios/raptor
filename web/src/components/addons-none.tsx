import { EmptyState } from "@/components/page";

// AddonsNone is shown at the tab's root when the server has no add-ons
// this user can manage.
export function AddonsNone() {
  return (
    <EmptyState
      title="No add-ons for this server"
      description="Its game doesn't take plugins, mods, datapacks, or Workshop items, or you don't have access to them."
    />
  );
}
