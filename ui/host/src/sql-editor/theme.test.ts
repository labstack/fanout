import { defaultCssVariablesResolver, DEFAULT_THEME, mergeMantineTheme } from "@mantine/core";
import axe from "axe-core";
import { expect, it } from "vitest";
import { fanoutCssVariables, fanoutTheme } from "../../../theme";
import { EditorView } from "@codemirror/view";
import { sqlHighlight, sqlTheme } from "./theme";

it.each(["light", "dark"] as const)("keeps all SQL token colors at 4.5:1 on normal and active lines in %s", scheme => {
  const theme = mergeMantineTheme(DEFAULT_THEME, fanoutTheme);
  const mantine = defaultCssVariablesResolver(theme);
  const variables: Record<string, string> = { ...mantine.variables, ...mantine[scheme], ...fanoutCssVariables(theme)[scheme] };
  const resolve = (value: string): string => value.startsWith("var(") ? resolve(variables[value.slice(4, -1)]) : value;
  type Color = { alpha: number; parseString(value: string): void };
  const color = (axe.commons as unknown as { color: {
    Color: new () => Color; flattenColors(fg: Color, bg: Color): Color; getContrast(bg: Color, fg: Color): number;
  } }).color;
  const parse = (value: string) => { const result = new color.Color(); result.parseString(resolve(value)); return result; };
  // The color the theme gives the placeholder, as CodeMirror mounts it.
  const view = new EditorView({ extensions: [sqlTheme], parent: document.body });
  const rules = [...document.adoptedStyleSheets ?? [], ...document.styleSheets].flatMap(sheet => [...sheet.cssRules].map(rule => rule.cssText));
  view.destroy();
  const placeholder = rules.join("\n").match(/\.cm-placeholder\s*\{[^}]*color:\s*(var\([^)]*\))/)?.[1] ?? "unset";
  const body = parse("var(--mantine-color-body)");
  const active = color.flattenColors(parse("var(--mantine-primary-color-light)"), body);
  const selected = color.flattenColors(parse("var(--mantine-color-iris-light-hover)"), body);
  for (const background of [body, active, selected]) {
    for (const foreground of ["var(--mantine-color-text)", ...sqlHighlight.specs.map(style => style.color!)]) {
      expect(color.getContrast(background, parse(foreground)), `${scheme}: ${foreground}`).toBeGreaterThanOrEqual(4.5);
    }
  }
  expect(color.getContrast(parse("var(--mantine-color-default)"), parse("var(--mantine-color-dimmed)"))).toBeGreaterThanOrEqual(4.5);
  // An empty editor's placeholder can sit on the active line.
  for (const background of [body, active, selected]) expect(color.getContrast(background, parse(placeholder)), `${scheme}: placeholder`).toBeGreaterThanOrEqual(4.5);
});
