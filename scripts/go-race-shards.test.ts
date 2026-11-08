import { expect, test } from "bun:test";

const root = new URL("..", import.meta.url).pathname;
const run = (...args: string[]) => Bun.spawnSync(["bash", "scripts/go-race-shards.sh", ...args], { cwd: root });
const lines = (out: ReturnType<typeof run>) => out.stdout.toString().split("\n").filter(Boolean);

test("the race shards cover every Go package exactly once", () => {
  const names: string[] = JSON.parse(run("json").stdout.toString());
  expect(names.at(-1)).toBe("rest");
  const sharded = names.flatMap(name => {
    const out = run(name);
    expect(out.exitCode).toBe(0);
    return lines(out);
  });
  const all = Bun.spawnSync(["go", "list", "./..."], { cwd: root }).stdout.toString().split("\n").filter(Boolean);
  expect(new Set(sharded).size).toBe(sharded.length);
  expect([...sharded].sort()).toEqual([...all].sort());
}, 60_000); // Seven `go list` runs.

test("an unknown race shard fails instead of testing nothing", () => {
  const out = run("missing");
  expect(out.exitCode).toBe(1);
  expect(out.stderr.toString()).toContain("unknown shard missing");
});
