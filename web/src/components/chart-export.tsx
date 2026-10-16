import { Check, Copy, Download } from "lucide-react";
import { createContext, type RefObject, useContext, useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { type ChartImage, copy, download } from "@/lib/chart-image";

// Saving a chart as a picture: a Graph holds its chart's latest data (the
// TimeChart in it puts it there), and its buttons draw the picture with the
// Graph's title and note, and what the page is about (ChartSubject).

export type ChartData = Omit<ChartImage, "title" | "subject" | "note">;

// ChartSubject is what the charts under it are about ("smp on Basement
// Box"), written on their pictures.
export const ChartSubject = createContext<string | undefined>(undefined);

// ChartSlot is where a TimeChart leaves its data for its Graph.
export const ChartSlot = createContext<{
  ref: RefObject<ChartData | undefined>;
  ready: () => void;
} | null>(null);

// useChartSlot hands a chart's data to its Graph, if it's in one.
export function useChartSlot(data: ChartData) {
  const slot = useContext(ChartSlot);
  if (slot) slot.ref.current = data;
  const ready = slot?.ready;
  useEffect(() => ready?.(), [ready]);
}

export function ExportButtons({
  title,
  note,
  chart,
}: {
  title: string;
  note?: string;
  chart: RefObject<ChartData | undefined>;
}) {
  const subject = useContext(ChartSubject);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");
  const image = (): ChartImage | undefined =>
    chart.current && { ...chart.current, title, note, subject };

  async function run(fn: (c: ChartImage) => Promise<void>, after?: () => void) {
    const c = image();
    if (!c) return;
    setError("");
    try {
      await fn(c);
      after?.();
    } catch {
      setError("Your browser didn't allow it. Save the picture instead.");
    }
  }

  return (
    <span className="ml-auto flex items-center gap-0.5">
      {error && <span className="mr-1 text-xs font-normal text-destructive">{error}</span>}
      <Button
        size="icon-xs"
        variant="ghost"
        aria-label={`Save the ${title} chart as a picture`}
        title="Save as a picture"
        onClick={() => run(download)}
      >
        <Download />
      </Button>
      {typeof ClipboardItem !== "undefined" && (
        <Button
          size="icon-xs"
          variant="ghost"
          aria-label={`Copy the ${title} chart as a picture`}
          title="Copy as a picture"
          onClick={() =>
            run(copy, () => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1500);
            })
          }
        >
          {copied ? <Check /> : <Copy />}
        </Button>
      )}
    </span>
  );
}
