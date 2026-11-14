import { cn } from "cn";
import { ImageUp, Trash2 } from "lucide-react";
import { type PointerEvent, type ReactNode, useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { message } from "@/lib/errors";
import { apiURL } from "@/lib/transport";

// Pictures (org logos, avatars, egg icons; docs/PANEL.md#images) are served
// by the API at their image ID, which never changes for a picture.
export function imageURL(id: string): string {
  return `${apiURL}/images/${id}.png`;
}

// initials are what stands in for a picture: the first letters of the first
// two words, or the first two letters.
export function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  return (
    (words.length > 1 ? (words[0]?.[0] ?? "") + (words[1]?.[0] ?? "") : name.slice(0, 2)) || "?"
  ).toUpperCase();
}

// Picture shows a stored picture, or the fallback when there's none or it
// doesn't load.
export function Picture({
  image,
  fallback,
  className,
}: {
  image?: string;
  fallback: ReactNode;
  className?: string;
}) {
  const [failed, setFailed] = useState("");
  if (!image || failed === image) return <>{fallback}</>;
  return (
    <img
      src={imageURL(image)}
      alt=""
      draggable={false}
      onError={() => setFailed(image)}
      className={cn("shrink-0 object-cover", className)}
    />
  );
}

const sizes = {
  sm: "size-6 text-[10px]",
  md: "size-8 text-xs",
  lg: "size-16 text-lg",
};

// Avatar is a person's picture, or their initials, in a circle.
export function Avatar({
  name,
  image,
  size = "md",
  className,
}: {
  name: string;
  image?: string;
  size?: keyof typeof sizes;
  className?: string;
}) {
  return (
    <Picture
      image={image}
      className={cn("rounded-full", sizes[size], className)}
      fallback={
        <span
          className={cn(
            "flex shrink-0 items-center justify-center rounded-full bg-muted font-semibold",
            sizes[size],
            className,
          )}
        >
          {initials(name)}
        </span>
      }
    />
  );
}

// OrgLogo is an org's logo, or its initials, in a rounded square.
export function OrgLogo({
  name,
  image,
  size = "md",
  className,
}: {
  name: string;
  image?: string;
  size?: keyof typeof sizes;
  className?: string;
}) {
  return (
    <Picture
      image={image}
      className={cn("rounded-md", sizes[size], className)}
      fallback={
        <span
          className={cn(
            "flex shrink-0 items-center justify-center rounded-md bg-muted font-mono font-semibold text-foreground",
            sizes[size],
            className,
          )}
        >
          {initials(name)}
        </span>
      }
    />
  );
}

// accepted is what the Panel takes (never SVG, which can carry script).
const accepted = "image/png,image/jpeg,image/gif,image/webp";

// The picture sent: square, at the size the Panel keeps.
const outputSize = 256;

// PicturePicker uploads or removes a picture, cropping it square first.
// save gets the cropped PNG, or null to remove the picture.
export function PicturePicker({
  preview,
  hasPicture,
  round,
  disabled,
  save,
}: {
  preview: ReactNode;
  hasPicture: boolean;
  round?: boolean;
  disabled?: boolean;
  save: (png: Uint8Array | null) => Promise<void>;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run(png: Uint8Array | null) {
    setBusy(true);
    setError("");
    try {
      await save(png);
      setFile(undefined);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="flex items-center gap-4">
      {preview}
      <div className="grid gap-2">
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={disabled || busy}
            onClick={() => input.current?.click()}
          >
            <ImageUp /> {hasPicture ? "Change" : "Upload"}
          </Button>
          {hasPicture && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={disabled || busy}
              onClick={() => run(null)}
            >
              <Trash2 /> Remove
            </Button>
          )}
        </div>
        <p className="text-xs text-muted-foreground">PNG, JPEG, GIF, or WebP. Shown square.</p>
        {error && !file && <p className="text-sm text-destructive">{error}</p>}
      </div>
      <input
        ref={input}
        type="file"
        accept={accepted}
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (f) {
            setError("");
            setFile(f);
          }
        }}
      />
      {file && (
        <CropDialog
          file={file}
          round={round}
          busy={busy}
          error={error}
          onCancel={() => setFile(undefined)}
          onCrop={run}
        />
      )}
    </div>
  );
}

// The crop area's size on screen, in CSS pixels.
const viewSize = 256;

// CropDialog picks the square part of a picture to keep: drag to move it,
// and zoom.
function CropDialog({
  file,
  round,
  busy,
  error,
  onCancel,
  onCrop,
}: {
  file: File;
  round?: boolean;
  busy: boolean;
  error: string;
  onCancel: () => void;
  onCrop: (png: Uint8Array) => void;
}) {
  const [bitmap, setBitmap] = useState<ImageBitmap>();
  const [loadError, setLoadError] = useState("");
  const [zoom, setZoom] = useState(1);
  // Where the picture's top left is in the square; unset: centred.
  const [offset, setOffset] = useState<{ x: number; y: number }>();
  const canvas = useRef<HTMLCanvasElement>(null);
  const drag = useRef<{ x: number; y: number; ox: number; oy: number }>(undefined);

  useEffect(() => {
    let alive = true;
    let made: ImageBitmap | undefined;
    // Photos are turned the way their camera says.
    createImageBitmap(file, { imageOrientation: "from-image" })
      .then((b) => {
        made = b;
        if (alive) setBitmap(b);
        else b.close();
      })
      .catch(() => alive && setLoadError("That file isn't a picture this browser can read."));
    return () => {
      alive = false;
      made?.close();
    };
  }, [file]);

  // The picture's scale and position: it always covers the square.
  const base = bitmap ? Math.max(viewSize / bitmap.width, viewSize / bitmap.height) : 1;
  const scale = base * zoom;
  const w = (bitmap?.width ?? 0) * scale;
  const h = (bitmap?.height ?? 0) * scale;
  const clamp = (o: { x: number; y: number }) => ({
    x: Math.min(0, Math.max(viewSize - w, o.x)),
    y: Math.min(0, Math.max(viewSize - h, o.y)),
  });
  const pos = clamp(offset ?? { x: (viewSize - w) / 2, y: (viewSize - h) / 2 });

  useEffect(() => {
    const c = canvas.current;
    const ctx = c?.getContext("2d");
    if (!c || !ctx || !bitmap) return;
    const ratio = window.devicePixelRatio || 1;
    c.width = viewSize * ratio;
    c.height = viewSize * ratio;
    ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
    ctx.clearRect(0, 0, viewSize, viewSize);
    ctx.drawImage(bitmap, pos.x, pos.y, w, h);
  }, [bitmap, pos.x, pos.y, w, h]);

  function setZoomKeepingCentre(z: number) {
    if (!bitmap) return;
    // Zoom around the middle of the square.
    const cx = (viewSize / 2 - pos.x) / scale;
    const cy = (viewSize / 2 - pos.y) / scale;
    const s = base * z;
    setZoom(z);
    setOffset({ x: viewSize / 2 - cx * s, y: viewSize / 2 - cy * s });
  }

  function down(e: PointerEvent<HTMLCanvasElement>) {
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY, ox: pos.x, oy: pos.y };
  }
  function move(e: PointerEvent<HTMLCanvasElement>) {
    const d = drag.current;
    if (!d) return;
    setOffset(clamp({ x: d.ox + e.clientX - d.x, y: d.oy + e.clientY - d.y }));
  }

  async function crop() {
    if (!bitmap) return;
    const out = document.createElement("canvas");
    out.width = outputSize;
    out.height = outputSize;
    const ctx = out.getContext("2d");
    if (!ctx) return;
    const k = outputSize / viewSize;
    ctx.imageSmoothingQuality = "high";
    ctx.drawImage(bitmap, pos.x * k, pos.y * k, w * k, h * k);
    const blob = await new Promise<Blob | null>((resolve) => out.toBlob(resolve, "image/png"));
    if (blob) onCrop(new Uint8Array(await blob.arrayBuffer()));
  }

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onCancel()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Crop the picture</DialogTitle>
          <DialogDescription>Drag to move it, and zoom to choose what's shown.</DialogDescription>
        </DialogHeader>
        {loadError ? (
          <p className="text-sm text-destructive">{loadError}</p>
        ) : (
          <div className="flex flex-col items-center gap-4">
            <canvas
              ref={canvas}
              onPointerDown={down}
              onPointerMove={move}
              onPointerUp={() => {
                drag.current = undefined;
              }}
              aria-label="The part of the picture that's kept"
              className={cn(
                "cursor-grab touch-none border bg-muted active:cursor-grabbing",
                round ? "rounded-full" : "rounded-lg",
              )}
              style={{ width: viewSize, height: viewSize }}
            />
            <label className="flex w-full max-w-64 items-center gap-3 text-sm text-muted-foreground">
              Zoom
              <input
                type="range"
                min={1}
                max={4}
                step={0.01}
                value={zoom}
                disabled={!bitmap}
                onChange={(e) => setZoomKeepingCentre(Number(e.target.value))}
                className="flex-1 accent-primary"
              />
            </label>
          </div>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
          <Button disabled={!bitmap || busy} onClick={crop}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
