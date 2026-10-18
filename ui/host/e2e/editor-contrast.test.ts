import axe from "axe-core";
import { defaultCssVariablesResolver, DEFAULT_THEME, mergeMantineTheme } from "@mantine/core";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fanoutCssVariables, fanoutThemeConfig } from "../../theme";
import { editorOverlapTargets, proveEditorText } from "./editor-contrast";

beforeEach(() => {
  Object.assign(window, { axe });
  document.body.innerHTML = `<style>* { background-color: transparent; }</style><div data-sql-editor><div class="cm-editor" style="background-color: white">
    <div class="cm-content"><div class="cm-line" id="line" style="color: black">SELECT <span style="color: #0d4a6d">1</span></div></div>
  </div></div>`;
});
afterEach(() => { vi.restoreAllMocks(); document.body.innerHTML = ""; });

function validationProblem(scheme: "light" | "dark") {
  const mantine = defaultCssVariablesResolver(mergeMantineTheme(DEFAULT_THEME, fanoutThemeConfig));
  const variables: Record<string, string> = { ...mantine.variables, ...mantine[scheme], ...fanoutCssVariables()[scheme] };
  const resolve = (value: string): string => value.startsWith("var(") ? resolve(variables[value.slice(4, -1)]) : value;
  // Mantine Alert's default variant paints a translucent semantic background;
  // its message uses --mantine-color-black/white according to the scheme.
  document.body.insertAdjacentHTML("beforeend", `<div role="dialog" style="background-color: ${resolve("var(--mantine-color-body)")}">
    <div data-panel-sql-editor><div role="alert" style="background-color: ${resolve("var(--mantine-color-bad-light)")}">
      <div style="color: ${resolve(`var(--mantine-color-${scheme === "light" ? "black" : "white"})`)}"><ul><li class="panel-sql-problem"></li></ul></div>
    </div></div></div>`);
  const problem = document.querySelector<HTMLElement>(".panel-sql-problem")!;
  problem.append("panels[0].sql", ": ", 'Binder Error: Referenced column "missing_column" not found in FROM clause!');
  // happy-dom has no layout: provide two wrapped text rectangles and the actual
  // browser hit-test result. The browser suite uses native geometry instead.
  vi.spyOn(Range.prototype, "getClientRects").mockReturnValue([new DOMRect(128, 460, 880, 20), new DOMRect(128, 481, 400, 20)] as unknown as DOMRectList);
  const hit = vi.spyOn(document, "elementFromPoint").mockReturnValue(problem);
  return { problem, hit };
}

it.each(["light", "dark"] as const)("proves wrapped validation text against the alert and opaque dialog in %s", scheme => {
  const { problem } = validationProblem(scheme);
  const [proof] = proveEditorText([".panel-sql-problem"]);
  expect(proof.samples.map(sample => sample.text)).toEqual(Array.from(problem.childNodes, text => text.textContent));
  expect(proof.samples).toHaveLength(3);
  expect(proof.ratio).toBeGreaterThanOrEqual(4.5);
  expect(proof.samples[0].bg).toBe(scheme === "light" ? "#fbd9dc" : "#380d11");
  expect(proof.samples[0].fg).toBe(scheme === "light" ? "#000000" : "#ffffff");
  problem.style.color = proof.samples[0].bg!;
  expect(proveEditorText([".panel-sql-problem"])[0].ratio).toBeCloseTo(1);
});

it("leaves validation text unresolved when any wrapped run is covered, offscreen or has no geometry", () => {
  const { hit } = validationProblem("light");
  const tooltip = document.createElement("div"); tooltip.className = "cm-tooltip"; document.body.append(tooltip);
  hit.mockReturnValueOnce(document.querySelector(".panel-sql-problem")).mockReturnValueOnce(tooltip);
  expect(proveEditorText([".panel-sql-problem"])[0].ratio).toBeNull();
  vi.mocked(Range.prototype.getClientRects).mockReturnValue([new DOMRect(-10, 460, 100, 20)] as unknown as DOMRectList);
  expect(proveEditorText([".panel-sql-problem"])[0].ratio).toBeNull();
  vi.mocked(Range.prototype.getClientRects).mockReturnValue([] as unknown as DOMRectList);
  expect(proveEditorText([".panel-sql-problem"])[0].ratio).toBeNull();
});

it.each([
  ['[role="dialog"]', "background-color: transparent"],
  ['[role="alert"]', "background-image: linear-gradient(white, black)"],
  ['[role="alert"]', "opacity: 0.5"],
  [".panel-sql-problem", "filter: brightness(0.5)"],
])("leaves validation text with unsupported styles unresolved on %s (%s)", (selector, style) => {
  validationProblem("light");
  document.querySelector(selector)!.setAttribute("style", style);
  expect(proveEditorText([".panel-sql-problem"])[0].ratio).toBeNull();
});

it("only proves validation items in the SQL dialog and rejects selected validation text", () => {
  const { problem } = validationProblem("light");
  const range = document.createRange(); range.selectNodeContents(problem);
  window.getSelection()!.addRange(range);
  expect(proveEditorText([".panel-sql-problem"])[0].ratio).toBeNull();
  window.getSelection()!.removeAllRanges();
  document.body.append(problem);
  expect(proveEditorText([".panel-sql-problem"])[0].ratio).toBeNull();
  document.querySelector("[data-panel-sql-editor]")!.append(problem);
  document.querySelector('[role="dialog"]')!.removeAttribute("role");
  expect(proveEditorText([".panel-sql-problem"])[0].ratio).toBeNull();
});

it("measures every text run and uses the weakest token as the line's proof", () => {
  const [proof] = proveEditorText(["#line"]);
  expect(proof.target).toBe(JSON.stringify(["#line"]));
  expect(proof.samples.map(sample => sample.text)).toEqual(["SELECT ", "1"]);
  expect(proof.samples[0].ratio).toBeCloseTo(21);
  expect(proof.ratio).toBe(proof.samples[1].ratio);
  expect(proof.ratio).toBeGreaterThanOrEqual(4.5);
  document.querySelector("span")!.style.color = "#cccccc";
  expect(proveEditorText(["#line"])[0].ratio).toBeLessThan(4.5);
});

it("composes translucent line and token backgrounds over the actual editor surface", () => {
  document.querySelector<HTMLElement>(".cm-editor")!.style.backgroundColor = "black";
  document.querySelector<HTMLElement>(".cm-line")!.style.backgroundColor = "rgba(255,255,255,0.5)";
  const span = document.querySelector("span")!;
  span.style.backgroundColor = "rgba(255,255,255,0.5)";
  const [proof] = proveEditorText(["#line"]);
  expect(proof.samples[0].bg).toBe("#808080");
  expect(proof.samples[1].bg).toBe("#c0c0c0");
});

it.each([
  [".cm-editor", "background-color: transparent"],
  [".cm-line", "background-image: linear-gradient(white, black)"],
  ["span", "opacity: 0.5"],
  [".cm-content", "filter: brightness(0.5)"],
  ["span", "mix-blend-mode: difference"],
])("leaves unsupported computed styles unresolved on %s (%s)", (selector, style) => {
  document.querySelector(selector)!.setAttribute("style", style);
  expect(proveEditorText(["#line"])[0].ratio).toBeNull();
});

it("leaves missing targets, unrelated text, empty lines and selections unresolved", () => {
  document.body.insertAdjacentHTML("beforeend", '<p id="outside">Outside</p>');
  document.querySelector(".cm-content")!.insertAdjacentHTML("beforeend", '<div class="cm-line" id="empty"><br></div>');
  expect(proveEditorText(["#missing", "#outside", "#empty"]).map(proof => proof.ratio)).toEqual([null, null, null]);
  const range = document.createRange(); range.selectNodeContents(document.querySelector("#line")!);
  window.getSelection()!.addRange(range);
  expect(proveEditorText(["#line"])[0].ratio).toBeNull();
  window.getSelection()!.removeAllRanges();
});

it("only considers single-selector overlap incompletes without other failed checks", () => {
  const node = { target: ["#line"], all: [], none: [], any: [{ data: { messageKey: "elmPartiallyObscuring" } }] } as unknown as axe.NodeResult;
  expect(editorOverlapTargets([node])).toEqual(["#line"]);
  expect(editorOverlapTargets([
    { ...node, target: ["iframe", "#line"] },
    { ...node, all: node.any }, { ...node, none: node.any }, { ...node, any: [] },
    { ...node, any: [{ ...node.any[0], data: { messageKey: "bgImage" } }] },
  ])).toEqual([]);
});
