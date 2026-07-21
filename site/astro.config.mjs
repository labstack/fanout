import { defineConfig } from "astro/config";
import mdx from "@astrojs/mdx";
import rehypeSlug from "rehype-slug";
import rehypeAutolinkHeadings from "rehype-autolink-headings";

export default defineConfig({
  site: "https://fanout.run",
  server: {
    // Local marketing/docs retain fanout.test; the application uses the
    // demo.fanout.test subdomain.
    allowedHosts: ["fanout.test"],
  },
  markdown: {
    // Pin github-dark-default over Astro's default github-dark: higher
    // contrast on yaml/json/http tokens against the docs' dark surface.
    shikiConfig: {
      theme: "github-dark-default",
      wrap: false,
    },
    // rehype-slug gives each heading an id; rehype-autolink-headings wraps
    // the heading in an <a href="#id"> so the TOC can deep-link. The wrap
    // behavior is paired with a `.docs-body h2 > a` style reset in
    // DocsLayout.astro — if you change the behavior here, update that too.
    rehypePlugins: [
      rehypeSlug,
      [rehypeAutolinkHeadings, { behavior: "wrap" }],
    ],
  },
  integrations: [mdx()],
});
