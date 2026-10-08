import { expect, test } from "bun:test";
test("the CI check executes every script test and the behavioral naming gate", () => {
  const out = Bun.spawnSync(["just", "--dry-run", "check"], { cwd: new URL("..", import.meta.url).pathname });
  expect(out.exitCode).toBe(0);
  const commands = out.stdout.toString() + out.stderr.toString();
  expect(commands).toMatch(/^bun test scripts\/\*\.test\.ts$/m);
  expect(commands).toContain("node scripts/test-names.mjs");
});
