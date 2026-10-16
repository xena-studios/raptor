import { CopyButton } from "@/components/address";
import { DetailList } from "@/components/page";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { shortId } from "@/lib/ids";

// IdsCard is a node's or server's two IDs: the short one in its URLs (and,
// for a node, its hostname), and the full one support and the CLI may ask
// for.
export function IdsCard({ id, what }: { id: string; what: "node" | "server" }) {
  const row = (value: string, label: string) => (
    <span className="inline-flex items-center gap-1">
      <code className="break-all">{value}</code>
      <CopyButton value={value} label={label} />
    </span>
  );
  return (
    <Card>
      <CardHeader>
        <CardTitle>IDs</CardTitle>
        <CardDescription>
          The short ID is the end of the full one. It's in this {what}'s page addresses
          {what === "node" ? " and its raptornodes.net hostname" : ""}; either works anywhere Raptor
          asks for an ID.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <DetailList
          rows={[
            ["Short ID", row(shortId(id), "short ID")],
            ["Full ID", row(id, "full ID")],
          ]}
        />
      </CardContent>
    </Card>
  );
}
