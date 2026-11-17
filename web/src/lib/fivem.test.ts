import assert from "node:assert/strict";
import { test } from "node:test";

import {
  counts,
  groupFindings,
  type ScanFinding,
  type ScanReport,
  scanSummary,
  splitPath,
  stoppedText,
  txAdminPin,
} from "./fivem.ts";

const f = (file: string, line: number, severity: ScanFinding["severity"]): ScanFinding => ({
  file,
  line,
  severity,
  rule: "r",
  title: "t",
  excerpt: "e",
});

test("findings are grouped by file, worst first", () => {
  const g = groupFindings([
    f("resources/a/x.lua", 9, "low"),
    f("resources/b/y.lua", 3, "medium"),
    f("resources/a/x.lua", 2, "high"),
    f("resources/c/z.lua", 1, "medium"),
  ]);
  assert.deepEqual(
    g.map((x) => [x.file, x.severity, x.findings.map((y) => y.line)]),
    [
      ["resources/a/x.lua", "high", [2, 9]],
      ["resources/b/y.lua", "medium", [3]],
      ["resources/c/z.lua", "medium", [1]],
    ],
  );
  assert.deepEqual(counts([f("a", 1, "high"), f("b", 1, "high"), f("c", 1, "low")]), {
    high: 2,
    medium: 0,
    low: 1,
  });
});

const report = (r: Partial<ScanReport>): ScanReport => ({
  dirs: ["resources", "txData"],
  files: 120,
  bytes: 1,
  skipped: 0,
  findings: [],
  ...r,
});

test("a scan in a sentence", () => {
  assert.equal(
    scanSummary(report({})),
    "Nothing suspicious in 120 scripts in resources and txData.",
  );
  assert.equal(scanSummary(report({ dirs: [] })), "There's no resources folder to scan yet.");
  assert.equal(
    scanSummary(
      report({ dirs: ["resources"], files: 1, findings: [f("a", 1, "high"), f("a", 2, "low")] }),
    ),
    "2 suspicious lines in 1 file (1 high, 1 low), from 1 script in resources.",
  );
  assert.equal(stoppedText(report({})), "");
  assert.equal(
    stoppedText(report({ stopped: "time", skipped: 2 })),
    "The scan stopped early: it ran out of time. 2 files over 4 MiB weren't read.",
  );
});

test("a file's folder and name", () => {
  assert.deepEqual(splitPath("resources/[local]/evil/server.lua"), {
    dir: "resources/[local]/evil",
    name: "server.lua",
  });
  assert.deepEqual(splitPath("x.lua"), { dir: "", name: "x.lua" });
});

test("txAdmin's first-run PIN", () => {
  // txAdmin's banner: a box, the PIN in inverse video on its own line.
  const banner = [
    "[         tx] ┏━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓",
    "[         tx] ┃ All ready! Please access:          ┃",
    "[         tx] ┃   http://127.0.0.1:40120/         ┃",
    "[         tx] ┃                                   ┃",
    "[         tx] ┃ Use the PIN below to register:    ┃",
    "[         tx] ┃               \x1b[7m 4821 \x1b[27m               ┃",
    "[         tx] ┗━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┛",
  ];
  assert.equal(txAdminPin(banner), "4821");
  assert.equal(txAdminPin(["[txAdmin] Use the PIN below to register:", "[txAdmin] 0937"]), "0937");
  assert.equal(txAdminPin(["Use the PIN below to register: 1234"]), "1234");
  assert.equal(txAdminPin(["http://10.0.0.2:40120/addMaster/pin#5678"]), "5678");
  // The newest one wins.
  assert.equal(txAdminPin([...banner, "Use the PIN below to register:", "  1111"]), "1111");
  // A time or a port isn't a PIN; neither is a banner without one.
  assert.equal(txAdminPin(["Use the PIN below to register:", "[12:34:56] started"]), undefined);
  assert.equal(txAdminPin(["All ready! Please access:", "  http://127.0.0.1:40120/"]), undefined);
  assert.equal(txAdminPin([]), undefined);
});
