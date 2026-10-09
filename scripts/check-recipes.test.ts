import { expect, test } from "bun:test";
test("the CI check executes every script test and the behavioral naming gate", () => {
  const out = Bun.spawnSync(["just", "--dry-run", "check"], { cwd: new URL("..", import.meta.url).pathname });
  expect(out.exitCode).toBe(0);
  const commands = out.stdout.toString() + out.stderr.toString();
  expect(commands).toMatch(/^bun test scripts\/\*\.test\.ts scripts\/dashboard-eval$/m);
  expect(commands).toContain("bun scripts/dashboard-eval/main.ts --mock --no-output");
  expect(commands).toContain("node scripts/test-names.mjs");
  expect(commands).toContain("just test ./internal/agent -count=1 -run TestDashboardEvalGoldensCurrent");
});
