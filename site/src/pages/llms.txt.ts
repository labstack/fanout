import type { APIRoute } from "astro";
import { getCollection } from "astro:content";

// The index an agent reads first.
//
// A list of titles tells an agent what a page is called. It does not tell it
// whether the page is worth fetching, which is the only question the agent has.
// So every entry carries the author's own one-line summary, and anything not
// yet shipped says so in the index rather than in a page the agent may never
// open.
//
// The landing page is its own group: splitting on the first path segment gives
// it "index", which a list of directory names does not contain, so without it
// the page would be dropped from an index whose own heading promises every one.
const ROOT = "index";
const ORDER = [ROOT, "start", "guides", "reference", "explanation", "status", "privacy"];

const GROUP_TITLES: Record<string, string> = {
  [ROOT]: "Overview",
  start: "Start here",
  guides: "Guides",
  reference: "Reference",
  explanation: "Explanation",
  status: "Status",
  privacy: "Site terms",
};

// Keyed by the status union rather than by string, so adding a fourth status is
// a compile error here instead of a page quietly advertised to an agent without
// its honesty marker.
type PageStatus = "shipped" | "preview" | "planned";

const STATUS_NOTE: Record<Exclude<PageStatus, "shipped">, string> = {
  preview:
    " [PREVIEW: this runs, but its shape may still change without a migration path]",
  planned:
    " [PLANNED: designed and written down, but nothing executes it yet]",
};

export const GET: APIRoute = async ({ site }) => {
  const origin = (site ?? new URL("https://fanout.run")).origin;
  const docs = await getCollection("docs");

  const lines: string[] = [
    "# Fanout",
    "",
    "One self-hosted binary for OpenTelemetry. The same Go process accepts OTLP",
    "over gRPC and HTTP, publishes authoritative Parquet batches with persistent",
    "trace indexes, runs embedded DuckDB analytics and rollups, evaluates alert",
    "rules, serves a chat investigator and MCP tools, and hosts the browser client.",
    "",
    "> Use this file as a map of the Fanout documentation. Fetch any page as",
    "> Markdown by appending `.md` to its URL. Fanout is pre-release: entries",
    "> marked PREVIEW run today but may change shape without a migration path,",
    "> and entries marked PLANNED do not execute yet. Trust the marker over the",
    "> confidence of the prose.",
    "",
    "## Agent Resources",
    "",
    `- [Markdown page export](${origin}/start/install.md): Append \`.md\` to any docs page URL for clean Markdown.`,
    `- [Full documentation text](${origin}/llms-full.txt): Every page concatenated, for one-shot ingestion.`,
    `- [Sitemap](${origin}/sitemap-index.xml): Crawler URL index.`,
    "",
    "## Operating Fanout from an agent",
    "",
    "- Fanout is a server. Its whole command line is three forms: `fanout [flags]` to run it, `fanout version`, and `fanout [--config path] login-link <email>` — a local-auth-mode recovery path that needs shell access on the host. Everything else is done over HTTP or MCP; there are no operational subcommands to script.",
    "- MCP is the intended agent interface. The server exposes its tools at `/mcp`.",
    "- MCP and the HTTP API authenticate with an API key, which is not the same credential as the ingest token. Both are `fo_`-prefixed; the routes tell them apart by prefix.",
    "- Telemetry goes in over OTLP only: gRPC on port 4317 and HTTP on port 4318, each its own listener. There is no bespoke ingest API to script against.",
    "- Ingest always requires the ingest token, and rejects everything until the first administrator exists — a collector started before setup will fail until setup completes.",
    "- Configuration is environment-first, and every variable is `FANOUT_`-prefixed. An unrecognised `FANOUT_` variable is a startup error rather than a silently ignored one.",
    "",
  ];

  // Silently dropping a section is how a whole directory of documentation
  // becomes invisible to every agent while the site builds clean and the
  // sidebar still shows it. Fail the build instead.
  const groups = new Set(docs.map((doc) => doc.id.split("/")[0] ?? ROOT));
  const unlisted = [...groups].filter((group) => !ORDER.includes(group));
  if (unlisted.length > 0) {
    throw new Error(
      `llms.txt would omit these documentation sections: ${unlisted.join(", ")}. ` +
        `Add them to ORDER and GROUP_TITLES in src/pages/llms.txt.ts.`,
    );
  }

  const unsummarised = docs.filter(
    (doc) => !doc.data.summary && !doc.data.description,
  );
  if (unsummarised.length > 0) {
    throw new Error(
      `these pages would be indexed with no summary, which is the one thing an ` +
        `agent needs to decide whether to fetch them: ${unsummarised
          .map((doc) => doc.id || "index")
          .join(", ")}`,
    );
  }

  const byGroup = new Map<string, typeof docs>();
  for (const doc of docs) {
    const group = doc.id.split("/")[0] ?? ROOT;
    const bucket = byGroup.get(group) ?? [];
    bucket.push(doc);
    byGroup.set(group, bucket);
  }

  for (const group of ORDER) {
    const entries = (byGroup.get(group) ?? []).sort((a, b) =>
      a.id.localeCompare(b.id),
    );
    if (entries.length === 0) continue;

    lines.push(`## ${GROUP_TITLES[group] ?? group}`, "");
    for (const entry of entries) {
      const title = entry.data.title;
      const summary = entry.data.summary ?? entry.data.description;
      const status = entry.data.status as PageStatus;
      const note = status === "shipped" ? "" : STATUS_NOTE[status];
      const path = entry.id;
      lines.push(`- [${title}](${origin}/${path}.md): ${summary}${note}`);

      for (const when of entry.data.read_when ?? []) {
        lines.push(`  - Read when: ${when}`);
      }
    }
    lines.push("");
  }

  return new Response(lines.join("\n"), {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
  });
};
