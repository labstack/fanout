import { expect, test } from "bun:test";
import { siteAssetURL } from "../site/src/lib/site-assets";
test("documentation requires a configured canonical site URL", () => {
  expect(() => siteAssetURL(undefined, "/social-card.png")).toThrow("Fanout site URL is required");
  expect(siteAssetURL(new URL("https://fanout.run"), "/social-card.png")).toBe("https://fanout.run/social-card.png");
  expect(siteAssetURL(new URL("https://preview.example/docs"), "/reference/storage.md")).toBe("https://preview.example/reference/storage.md");
});
