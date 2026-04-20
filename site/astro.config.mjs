import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import mdx from "@astrojs/mdx";

export default defineConfig({
  site: "https://fanout.run",
  server: {
    allowedHosts: ["fanout.test"],
  },
  integrations: [
    starlight({
      title: "Fanout",
      description:
        "Single-binary OpenTelemetry ingest, storage, and query — self-hosted.",
      customCss: ["./src/styles/theme.css"],
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/labstack/fanout",
        },
      ],
      sidebar: [
        {
          label: "Start here",
          items: [
            { label: "Introduction", slug: "docs/introduction" },
            { label: "Install", slug: "docs/install" },
            { label: "Getting started", slug: "docs/getting-started" },
          ],
        },
        {
          label: "Configuration",
          items: [
            { label: "Environment", slug: "docs/config" },
            { label: "OTLP ingest", slug: "docs/ingest" },
          ],
        },
        {
          label: "Features",
          items: [
            { label: "Architecture", slug: "docs/architecture" },
            { label: "MCP server", slug: "docs/mcp" },
            { label: "Alerts", slug: "docs/alerts" },
          ],
        },
      ],
    }),
    mdx(),
  ],
});
