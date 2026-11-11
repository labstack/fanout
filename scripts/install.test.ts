import { expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { spawnSync } from "node:child_process";

const installer = resolve(import.meta.dir, "install.sh");
for (const legacy of [true, false]) {
  test(`installer verifies renamed archives with ${legacy ? "legacy fallback" : "primary precedence"}`, () => {
    const root = mkdtempSync(join(tmpdir(), "pitot-install-"));
    try {
      const commands = join(root, "commands"), stage = join(root, "stage"), prefix = join(root, "installed");
      mkdirSync(commands); mkdirSync(stage);
      writeFileSync(join(stage, "pitot"), "#!/bin/sh\necho fixture\n");
      const os = process.platform === "darwin" ? "darwin" : "linux";
      const arch = process.arch === "arm64" ? "arm64" : "amd64";
      const archive = `pitot_v2026.11.4-alpha_${os}_${arch}.tar.gz`;
      expect(spawnSync("tar", ["-czf", join(root, archive), "-C", stage, "./pitot"]).status).toBe(0);
      const hash = createHash("sha256").update(readFileSync(join(root, archive))).digest("hex");
      writeFileSync(join(root, "SHA256SUMS"), `${hash}  ${archive}\n`);
      writeFileSync(join(commands, "curl"), `#!/bin/sh
case "$2" in
  https://github.com/labstack/pitot/releases/download/v2026.11.4-alpha/*) cp "$INSTALL_FIXTURES/\${2##*/}" "$4" ;;
  *) exit 10 ;;
esac
`);
      chmodSync(join(commands, "curl"), 0o700);
      const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !/^(PITOT|FANOUT)_/.test(key)));
      Object.assign(env, { PATH: `${commands}:${process.env.PATH}`, INSTALL_FIXTURES: root,
        FANOUT_VERSION: legacy ? "v2026.11.4-alpha" : "wrong-tag", FANOUT_PREFIX: legacy ? prefix : join(root, "wrong-prefix") });
      if (!legacy) Object.assign(env, { PITOT_VERSION: "v2026.11.4-alpha", PITOT_PREFIX: prefix });
      const result = spawnSync("sh", [installer], { env, encoding: "utf8" });
      expect(result.status).toBe(0);
      expect(readFileSync(join(prefix, "pitot"), "utf8")).toContain("fixture");
      expect(result.stderr.match(/deprecated environment variables/g)?.length ?? 0).toBe(legacy ? 1 : 0);
      if (legacy) expect(result.stderr).toContain("FANOUT_VERSION, FANOUT_PREFIX");
      expect(result.stderr).not.toContain(prefix);
    } finally { rmSync(root, { recursive: true, force: true }); }
  });
}
