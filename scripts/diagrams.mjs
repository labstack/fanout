import { spawnSync } from "node:child_process";
import { access, copyFile, mkdir, mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { createRequire } from "node:module";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ARCHIFY_VERSION = "3.1.0";
const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const evidenceRoot = resolve(process.env.DIAGRAMS_EVIDENCE_ROOT || repo);
const diagrams = join(repo, "docs/diagrams");
const archify = resolve(process.env.ARCHIFY_HOME || join(homedir(), ".agents/skills/archify"));

async function main() {
  const sources = [];
  for (const source of (await readdir(diagrams)).filter(name => name.endsWith(".json")).sort()) {
    const input = join(diagrams, source);
    const { diagram_type: type } = JSON.parse(await readFile(input, "utf8"));
    if (typeof type !== "string" || !type) {
      throw new Error(`${input} must declare diagram_type.`);
    }
    sources.push({ source, input, type });
  }

  const cli = join(archify, "bin/archify.mjs");
  try {
    await access(cli);
  } catch {
    throw new Error(`Archify is missing ${cli}; set ARCHIFY_HOME to a local archify ${ARCHIFY_VERSION} install.`);
  }
  let release;
  try {
    release = JSON.parse(await readFile(join(archify, "skill-release.json"), "utf8"));
  } catch {
    throw new Error(`Cannot read archify's skill-release.json in ${archify}; archify ${ARCHIFY_VERSION} is required.`);
  }
  if (release?.version !== ARCHIFY_VERSION) {
    throw new Error(`Archify ${ARCHIFY_VERSION} is required; ${archify} reports version ${JSON.stringify(release?.version)}.`);
  }

  const require = createRequire(join(repo, "ui/host/package.json"));
  const { chromium } = require("@playwright/test");
  let browser;
  try {
    for (const { source, input, type } of sources) {
      const name = source.slice(0, -".json".length);
      const temporary = await mkdtemp(join(tmpdir(), "fanout-diagram-"));
      try {
        const html = join(temporary, `${name}.html`);
        const result = spawnSync(process.execPath, [cli, "finalize", type, input, html,
          "--repo-root", evidenceRoot, "--quality", "showcase", "--json"], {
          cwd: repo,
          encoding: "utf8",
          maxBuffer: 16 * 1024 * 1024,
        });
        if (result.error || result.status !== 0) {
          // The JSON receipt contains the failed gate's diagnostics.
          if (result.stdout) console.error(result.stdout.trim());
          if (result.stderr) console.error(result.stderr.trim());
          let receipt;
          try {
            receipt = JSON.parse(result.stdout);
          } catch {
            // Preserve the original failure when no JSON receipt is available.
          }
          if (Array.isArray(receipt?.diagnostics) && receipt.diagnostics.some(
            diagnostic => typeof diagnostic?.code === "string" && diagnostic.code.startsWith("repository-evidence/"),
          )) {
            console.error("Set DIAGRAMS_EVIDENCE_ROOT to a clone of labstack/fanout that contains the pinned revision.");
          }
          throw new Error(`Archify finalize failed for ${source}: ${result.error?.message ?? result.signal ?? `exit ${result.status}`}`);
        }
        browser ??= await chromium.launch({ channel: "chrome", headless: true });
        const context = await browser.newContext({ acceptDownloads: true });
        try {
          const page = await context.newPage();
          await page.goto(pathToFileURL(html).href);
          await page.getByRole("button", { name: "Export diagram", exact: true }).click();
          const [download] = await Promise.all([
            page.waitForEvent("download"),
            page.getByRole("menuitem", { name: /^SVG · Auto/ }).click(),
          ]);
          await download.saveAs(join(diagrams, `${name}.svg`));
        } finally {
          await context.close();
        }
      } finally {
        await rm(temporary, { recursive: true, force: true });
      }
    }
    const destination = join(repo, "site/public/diagrams");
    await mkdir(destination, { recursive: true });
    for (const svg of (await readdir(diagrams)).filter(name => name.endsWith(".svg")).sort()) {
      await copyFile(join(diagrams, svg), join(destination, svg));
    }
  } finally {
    await browser?.close();
  }
}

try {
  await main();
} catch (error) {
  console.error(error instanceof Error ? error.message : error);
  process.exitCode = 1;
}
