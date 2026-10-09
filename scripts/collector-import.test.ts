import { expect, test } from "bun:test";
import { existsSync } from "node:fs";
const collector = new URL("../.superpowers/replay/m2-report.ts", import.meta.url).pathname;
test.skipIf(!existsSync(collector))("the private browser evidence reporter resolves its shared helpers", async () => {
  const out = await Bun.build({ entrypoints: [collector], target: "bun", write: false });
  expect(out.success).toBe(true);
});
