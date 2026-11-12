import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

// These published URL fixtures are intentional rename compatibility exceptions.
const previousReference = "/docs/reference/fanql";
const currentReference = "/docs/reference/pitotql";

test("published query-reference URLs redirect HTML and Markdown to the current page", () => {
  const redirects = readFileSync(new URL("../site/public/_redirects", import.meta.url), "utf8")
    .split("\n").filter(line => line && !line.startsWith("#")).map(line => line.trim().split(/\s+/));
  expect(redirects).toContainEqual([previousReference, currentReference, "301"]);
  expect(redirects).toContainEqual([previousReference + ".html", currentReference, "301"]);
  expect(redirects).toContainEqual([previousReference + ".md", currentReference + ".md", "301"]);
  const navigation = readFileSync(new URL("../site/astro.config.mjs", import.meta.url), "utf8");
  expect(navigation).toContain('slug: "docs/reference/pitotql"');
  expect(navigation).not.toContain(previousReference);
});
