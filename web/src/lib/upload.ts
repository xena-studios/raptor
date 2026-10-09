import { join } from "@/lib/files";
import { apiURL, commandClient } from "@/lib/transport";

// Uploads through the Panel (internal/panel/transfers): files.upload starts
// one on the node, then chunks go by PUT, each at the offset the node has
// received up to, so a dropped connection resumes where it left off.

const chunkSize = 16 << 20; // well under Cloudflare's 100 MB request limit
const maxFailures = 6;

type UploadState = {
  upload_id?: string;
  received: number;
  size?: number;
  done?: boolean;
  max_chunk?: number;
  error?: string;
};

export async function uploadFile(opts: {
  nodeId: string;
  serverId: string;
  dir: string;
  file: File;
  onProgress: (fraction: number) => void;
  signal: AbortSignal;
}): Promise<void> {
  const { nodeId, serverId, file } = opts;
  const res = await commandClient.execute({
    nodeId,
    action: "files.upload",
    serverId,
    paramsJson: JSON.stringify({ path: join(opts.dir, file.name), size: file.size }),
  });
  const start = JSON.parse(res.resultJson || "{}") as UploadState;
  if (!start.upload_id) throw new Error("The node didn't start the upload.");
  const id = start.upload_id;
  const size = Math.min(start.max_chunk || chunkSize, chunkSize);
  let offset = start.received ?? 0;
  let done = !!start.done;
  let failures = 0;

  while (!done) {
    const body = file.slice(offset, offset + size);
    const url = `${apiURL}/files/upload?${new URLSearchParams({
      node: nodeId,
      server: serverId,
      upload: id,
      offset: String(offset),
    })}`;
    let r: Response;
    let st: UploadState;
    try {
      r = await fetch(url, { method: "PUT", body, credentials: "include", signal: opts.signal });
      st = (await r.json()) as UploadState;
    } catch (err) {
      if (opts.signal.aborted) throw err;
      if (++failures > maxFailures) throw new Error("The upload keeps failing. Try again later.");
      await sleep(500 * 2 ** failures);
      continue;
    }
    if (r.ok || r.status === 409) {
      // 409: the node has a different offset (a chunk landed but its answer
      // was lost); go on from there.
      offset = st.received;
      done = !!st.done;
      failures = 0;
      opts.onProgress(file.size ? offset / file.size : 1);
      continue;
    }
    if (r.status >= 500 && ++failures <= maxFailures) {
      await sleep(500 * 2 ** failures);
      continue;
    }
    throw new Error(st.error || "The upload failed. Try again in a moment.");
  }
  opts.onProgress(1);
}

// downloadURL is where the browser fetches a file from, whole.
export function downloadURL(nodeId: string, serverId: string, path: string): string {
  return `${apiURL}/files/download?${new URLSearchParams({ node: nodeId, server: serverId, path })}`;
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}
