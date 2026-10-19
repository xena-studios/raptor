import assert from "node:assert/strict";
import { test } from "node:test";

import { csvText } from "./csv.ts";

test("CSV quotes what needs it, and NULL is empty", () => {
  assert.equal(
    csvText({
      columns: [{ name: "id" }, { name: "name" }],
      rows: [
        ["1", 'sam, "the" builder'],
        ["2", null],
        ["3", "two\nlines"],
      ],
    }),
    'id,name\r\n1,"sam, ""the"" builder"\r\n2,\r\n3,"two\nlines"\r\n',
  );
});
