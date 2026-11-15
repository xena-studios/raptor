import assert from "node:assert/strict";
import { test } from "node:test";

import { isRaptorFile, pterodactylFile } from "./egg-export.ts";

test("a Raptor egg exported for Pterodactyl", () => {
  const raptor = JSON.stringify({
    meta: { version: "RPTR_v1" },
    name: "Paper",
    library: "minecraft/paper",
    arch: ["amd64"],
    docker_images: { "Java 21": "x" },
    startup_commands: { Default: "java -jar a.jar", Other: "b" },
    image: "data:image/png;base64,AA==",
  });
  assert.equal(isRaptorFile(raptor), true);
  const out = JSON.parse(pterodactylFile(raptor));
  assert.deepEqual(out.meta, { version: "PTDL_v2", update_url: null });
  assert.equal(out.startup, "java -jar a.jar");
  assert.equal(out.startup_commands, undefined);
  assert.deepEqual(out["x-raptor"], { library: "minecraft/paper", arch: ["amd64"] });
  assert.equal(out.arch, undefined);
  assert.equal(out.image, "data:image/png;base64,AA==");
  assert.equal(isRaptorFile(JSON.stringify(out)), false);
  assert.equal(isRaptorFile("meta:\n  version: PLCN_v3\n"), false);
});
