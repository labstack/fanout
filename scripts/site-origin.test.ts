import { expect, test } from "bun:test";
import { siteAssetURL } from "../site/src/lib/site-assets";
test("documentation requires a configured canonical site URL", () => {
  expect(() => siteAssetURL(undefined, "/social-card.png")).toThrow("Pitot site URL is required");
  expect(siteAssetURL(new URL("https://pitot.dev"), "/social-card.png")).toBe("https://pitot.dev/social-card.png");
  expect(siteAssetURL(new URL("https://preview.example/docs"), "/docs/reference/configuration/storage.md")).toBe("https://preview.example/docs/reference/configuration/storage.md");
});
