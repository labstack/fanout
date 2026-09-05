import { describe, expect, it } from "vitest";
import { fanoutThemeConfig } from "../../theme";
import { fonts } from "../../tokens";

describe("typeface", () => {
  it("sets one grotesk for UI and headings and mono for data", () => {
    expect(fonts.body).toMatch(/^"Geist Variable"/);
    expect(fonts.display).toMatch(/^"Geist Mono Variable"/);
    expect(fanoutThemeConfig.fontFamily).toBe(fonts.body);
    expect(fanoutThemeConfig.headings.fontFamily).toBe(fonts.body);
    expect(fanoutThemeConfig.headings.fontWeight).toBe("600");
    expect(fanoutThemeConfig.fontFamilyMonospace).toBe(fonts.display);
  });
});
