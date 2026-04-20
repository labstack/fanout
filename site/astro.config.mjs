import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import mdx from "@astrojs/mdx";

export default defineConfig({
  site: "https://fanout.run",
  server: {
    // Local dev keeps the public site on fanout.test; demo.fanout.test is
    // reserved for the Vite-powered app shell.
    allowedHosts: ["fanout.test"],
  },
  integrations: [
    starlight({
      title: "Fanout",
      description:
        "Observability that runs anywhere you can run a binary. OpenTelemetry ingest, fast UI, chat investigator — self-hosted.",
      customCss: ["./src/styles/theme.css"],
      // Dark-only site — pin one dark Shiki theme for every code block (not
      // just terminal frames) so non-bash samples (yaml, json, http, text)
      // stay readable.
      expressiveCode: {
        themes: ["github-dark-default"],
      },
      head: [
        { tag: "link", attrs: { rel: "icon", type: "image/svg+xml", href: "/favicon.svg" } },
        { tag: "meta", attrs: { property: "og:type", content: "website" } },
        { tag: "meta", attrs: { property: "og:image", content: "https://fanout.run/og.png" } },
        { tag: "meta", attrs: { name: "twitter:card", content: "summary_large_image" } },
        { tag: "meta", attrs: { name: "twitter:image", content: "https://fanout.run/og.png" } },
      ],
      components: {
        Head: "./src/components/StarlightHead.astro",
      },
      // Single-page docs — the "On this page" TOC handles navigation.
      sidebar: [{ label: "Fanout docs", slug: "docs" }],
    }),
    mdx(),
  ],
});
