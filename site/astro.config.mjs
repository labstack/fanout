// @ts-check
import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import { rehypeTableWrap } from "./src/plugins/rehype-table-wrap.mjs";
import { fanoutCodeDark, fanoutCodeLight } from "./src/styles/code-theme.mjs";

// The site URL is what makes llms.txt and the Markdown alternates absolute.
// An agent that resolves a relative link against the wrong origin fetches
// nothing, so this is set rather than inferred.
const SITE = process.env.SITE_URL ?? "https://fanout.run";

export default defineConfig({
  site: SITE,
  trailingSlash: "never",
  build: { format: "file" },
  markdown: { rehypePlugins: [rehypeTableWrap] },
  integrations: [
    starlight({
      title: "Fanout",
      // The separator between a page's title and the site's: "Install · Fanout".
      titleDelimiter: "·",
      description:
        "One binary that ingests OpenTelemetry, stores it, and answers questions about it.",
      // The project tagline, in the same words as the GitHub description and the
      // landing page. Starlight renders this only on a splash page that does not
      // supply its own hero tagline; index.mdx supplies one, so today this has
      // no output. It is kept in step anyway, because the day a second splash
      // page exists is not the day to discover the tagline drifted.
      tagline: "Observability that runs anywhere you can run a binary.",
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/labstack/fanout",
        },
      ],
      editLink: {
        baseUrl: "https://github.com/labstack/fanout/edit/main/site/",
      },
      // Expressive Code's syntax palette is independent of Starlight's grey
      // tokens, so without naming themes here the code blocks keep GitHub's
      // colours inside an Ayu frame.
      expressiveCode: {
        themes: [fanoutCodeDark, fanoutCodeLight],
        styleOverrides: { borderColor: "var(--sl-color-gray-5)" },
      },
      customCss: ["./src/styles/fanout.css"],
      components: {
        // Advertises the Markdown alternate for the current page, so an agent
        // that reads the HTML head never has to parse the HTML body.
        Head: "./src/components/Head.astro",
        // Starlight renders Hero in place of a page title for any page that
        // declares `hero` in its frontmatter. The landing page is the only one
        // that does, so this override is effectively scoped to it — but the
        // registration is global, and a second splash page with a hero would
        // get the landing page's layout rather than Starlight's.
        Hero: "./src/components/landing/Hero.astro",
        // Starlight's own footer, minus the documentation affordances on the
        // splash page.
        Footer: "./src/components/Footer.astro",
        // Adds the site footer band below the content, and lets the main frame
        // take the slack so it sits at the bottom of a short page.
        PageFrame: "./src/components/PageFrame.astro",
      },
      lastUpdated: true,
      pagination: true,
      favicon: "/favicon.svg",
      sidebar: [
        {
          label: "Start here",
          items: [
            { label: "What Fanout is", slug: "start/what-fanout-is" },
            { label: "Install", slug: "start/install" },
            { label: "First boot", slug: "start/first-boot" },
            { label: "Send your first telemetry", slug: "start/send-telemetry" },
          ],
        },
        // Guides, the generated settings reference and the explanation section
        // are landing in later passes. A sidebar entry for a page that does not
        // exist fails the build, so this list grows with the tree rather than
        // ahead of it — an empty section is a promise, and a promise in a
        // sidebar is indistinguishable from a broken link.
        {
          label: "Status",
          items: [{ label: "Shipped vs planned", slug: "status/capabilities" }],
        },
      ],
    }),
  ],
});
