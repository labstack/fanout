import { defaultCssVariablesResolver, DEFAULT_THEME, mergeMantineTheme } from "@mantine/core";
import axe from "axe-core";
import { expect, it } from "vitest";
import { fanoutCssVariables, fanoutThemeConfig } from "../../../theme";
import { sqlHighlight } from "./theme";

it.each(["light", "dark"] as const)("keeps all SQL token colors at 4.5:1 on normal and active lines in %s", scheme => {
  const mantine = defaultCssVariablesResolver(mergeMantineTheme(DEFAULT_THEME, fanoutThemeConfig));
  const variables: Record<string, string> = { ...mantine.variables, ...mantine[scheme], ...fanoutCssVariables()[scheme] };
  const resolve = (value: string): string => value.startsWith("var(") ? resolve(variables[value.slice(4, -1)]) : value;
  type Color = { alpha: number; parseString(value: string): void };
  const color = (axe.commons as unknown as { color: {
    Color: new () => Color; flattenColors(fg: Color, bg: Color): Color; getContrast(bg: Color, fg: Color): number;
  } }).color;
  const parse = (value: string) => { const result = new color.Color(); result.parseString(resolve(value)); return result; };
  const body = parse("var(--mantine-color-body)");
  const active = color.flattenColors(parse("var(--mantine-primary-color-light)"), body);
  for (const background of [body, active]) {
    for (const foreground of ["var(--mantine-color-text)", ...sqlHighlight.specs.map(style => style.color!)]) {
      expect(color.getContrast(background, parse(foreground)), `${scheme}: ${foreground}`).toBeGreaterThanOrEqual(4.5);
    }
  }
  expect(color.getContrast(parse("var(--mantine-color-default)"), parse("var(--mantine-color-dimmed)"))).toBeGreaterThanOrEqual(4.5);
});
