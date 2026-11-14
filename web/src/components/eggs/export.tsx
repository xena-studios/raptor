import { ChevronDown, Download } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { isRaptorFile, pterodactylFile } from "@/lib/egg-export";
import { download } from "@/lib/eggs";

// ExportButton downloads an egg's file. One of Raptor's own eggs can also
// be exported as a Pterodactyl egg, which other panels import.
export function ExportButton({ name, file }: { name: string; file: Uint8Array }) {
  const text = new TextDecoder().decode(file);
  if (!isRaptorFile(text)) {
    return (
      <Button variant="outline" onClick={() => download(name, file)}>
        <Download /> Export
      </Button>
    );
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" />}>
        <Download /> Export <ChevronDown />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => download(name, file)}>Raptor egg</DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => download(name, new TextEncoder().encode(pterodactylFile(text)))}
        >
          Pterodactyl egg, for other panels
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
