import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
const head = readFileSync(new URL("../site/src/components/Head.astro", import.meta.url), "utf8");
const originCode = head.slice(head.indexOf("if (!Astro.site)"), head.indexOf("---", head.indexOf("if (!Astro.site)")));
const socialCard = new Function("Astro", originCode + "return socialCardHref;");
test("documentation requires a configured canonical site URL", () => {
  expect(() => socialCard({})).toThrow("Fanout site URL is required");
  expect(socialCard({ site: new URL("https://fanout.dev"), url: new URL("https://preview.invalid/docs") })).toBe("https://fanout.dev/social-card.png");
});
