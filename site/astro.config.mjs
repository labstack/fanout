import { defineConfig } from "astro/config";
import mdx from "@astrojs/mdx";
import rehypeSlug from "rehype-slug";
import rehypeAutolinkHeadings from "rehype-autolink-headings";

export default defineConfig({
  site: "https://fanout.run",
  server: {
    // Local dev keeps the public site on fanout.test; demo.fanout.test is
    // reserved for the Vite-powered app shell.
    allowedHosts: ["fanout.test"],
  },
  markdown: {
    // Single dark Shiki theme keeps non-bash code (yaml, json, http) readable;
    // the paired light theme that Astro would otherwise use renders near-black
    // tokens on the docs' dark surface.
    shikiConfig: {
      theme: "github-dark-default",
      wrap: false,
    },
    // Heading IDs + the TOC need anchors; autolink is a nice-to-have for
    // deep-linking from the marketing site.
    rehypePlugins: [
      rehypeSlug,
      [rehypeAutolinkHeadings, { behavior: "wrap" }],
    ],
  },
  integrations: [mdx()],
});
