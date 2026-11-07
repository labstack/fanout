import { expect, it } from "vitest";
import { contrastRatio } from "@labstack/ui";
import { bad, warn, surfaces } from "../../../tokens";
import { chart, series } from "../../../theme";

type Triple = [number, number, number];
type Mode = "normal" | "protan" | "deutan" | "tritan";
const modes: Mode[] = ["normal", "protan", "deutan", "tritan"];
// Machado, Oliveira & Fernandes (2009), A Physiologically-based Model for
// Simulation of Color Vision Deficiency, doi:10.1109/TVCG.2009.113.
// Severity 1.0 matrices verified against the authors' numerical table:
// https://www.inf.ufrgs.br/~oliveira/pubs_files/CVD_Simulation/CVD_Simulation.html
// Implementation written here from the published method, on linear sRGB.
const matrices: Record<Exclude<Mode, "normal">, Triple[]> = {
  protan: [[.152286, 1.052583, -.204868], [.114503, .786281, .099216], [-.003882, -.048116, 1.051998]],
  deutan: [[.367322, .860646, -.227968], [.280085, .672501, .047413], [-.011820, .042940, .968881]],
  tritan: [[1.255528, -.076749, -.178779], [-.078411, .930809, .147602], [.004733, .691367, .303900]],
};
const dot = (a: Triple, b: Triple) => a.reduce((sum, x, i) => sum + x * b[i], 0);
const linearize = (x: number) => x <= .04045 ? x / 12.92 : ((x + .055) / 1.055) ** 2.4;
function decode(hex: string): Triple {
  if (!/^#[\da-f]{6}$/i.test(hex)) throw new Error(`Expected opaque six-digit hex: ${hex}`);
  return [1, 3, 5].map(i => linearize(parseInt(hex.slice(i, i + 2), 16) / 255)) as Triple;
}
function simulate(rgb: Triple, mode: Mode): Triple {
  return mode === "normal" ? rgb : matrices[mode].map(row => Math.min(1, Math.max(0, dot(row, rgb)))) as Triple;
}
// Linear sRGB -> OKLab, from Björn Ottosson's published OKLab definition:
// https://bottosson.github.io/posts/oklab/ (no intermediate gamma encoding).
function lab(rgb: Triple): Triple {
  const lms: Triple = [
    Math.cbrt(dot([.4122214708, .5363325363, .0514459929], rgb)),
    Math.cbrt(dot([.2119034982, .6806995451, .1073969566], rgb)),
    Math.cbrt(dot([.0883024619, .2817188376, .6299787005], rgb)),
  ];
  return [dot([.2104542553, .7936177850, -.0040720468], lms),
    dot([1.9779984951, -2.4285922050, .4505937099], lms),
    dot([.0259040371, .7827717662, -.8086757660], lms)];
}
const distance = (a: Triple, b: Triple) => Math.hypot(...a.map((x, i) => x - b[i])) * 100;
const luminance = (rgb: Triple) => dot([.2126, .7152, .0722], rgb);
const contrast = (a: Triple, b: Triple) => (Math.max(luminance(a), luminance(b)) + .05) / (Math.min(luminance(a), luminance(b)) + .05);
type Pair = { a: number; b: number } & Record<Mode, number>;
type Measurements = { pairs: Pair[]; chromas: number[]; surface_contrast: Record<Mode, number>[] };
function measurePalette(colors: readonly string[], surface: string): Measurements {
  if (colors.length !== 6) throw new Error("Expected six ordered slots");
  const rgb = colors.map(decode), background = decode(surface);
  const transformed = Object.fromEntries(modes.map(mode => [mode, rgb.map(color => lab(simulate(color, mode)))])) as Record<Mode, Triple[]>;
  return {
    pairs: colors.slice(1).map((_, a) => ({ a, b: a + 1, ...Object.fromEntries(modes.map(mode => [mode, distance(transformed[mode][a], transformed[mode][a + 1])])) } as Pair)),
    chromas: transformed.normal.map(([, a, b]) => Math.hypot(a, b)),
    surface_contrast: rgb.map(color => Object.fromEntries(modes.map(mode => [mode, contrast(simulate(color, mode), simulate(background, mode))])) as Record<Mode, number>),
  };
}
type Finding = { rule: string; a?: number; b?: number; slot?: number; value: number };
function checkMeasurements(measurements: Measurements) {
  const failures: Finding[] = [], warnings: Finding[] = [];
  for (const { a, b, normal, protan, deutan } of measurements.pairs) {
    const value = Math.min(protan, deutan);
    if (value < 6) failures.push({ rule: "cvd_distance", a, b, value });
    else if (value < 8) warnings.push({ rule: "cvd_distance", a, b, value });
    if (normal < 15) failures.push({ rule: "normal_distance", a, b, value: normal });
  }
  measurements.chromas.forEach((value, slot) => {
    if (value < .10) failures.push({ rule: "slot_chroma", slot, value });
  });
  return { failures, warnings };
}

it.each(["light", "dark"] as const)("checks six ordered slots against the %s chart surface", theme => {
  expect(series[theme]).toHaveLength(6);
  const measurements = measurePalette(series[theme], chart[theme].surface);
  expect(measurements.pairs.map(p => [p.a, p.b])).toEqual([[0, 1], [1, 2], [2, 3], [3, 4], [4, 5]]);
  const verdict = checkMeasurements(measurements);
  console.log(JSON.stringify({ theme, surface: chart[theme].surface, measurements, ...verdict }));
  expect(verdict.failures).toEqual([]);
});

const boundary = (): Measurements => ({ pairs: [{ a: 0, b: 1, normal: 15, protan: 6, deutan: 8, tritan: 0 }], chromas: Array(6).fill(.10), surface_contrast: [] });
it("fails each binding threshold independently", () => {
  for (const mode of ["protan", "deutan"] as const) {
    const measurements = boundary(); measurements.pairs[0][mode] = 5.99;
    expect(checkMeasurements(measurements).failures.map(f => f.rule)).toEqual(["cvd_distance"]);
  }
  const normal = boundary(); normal.pairs[0].normal = 14.99;
  expect(checkMeasurements(normal).failures.map(f => f.rule)).toEqual(["normal_distance"]);
  const chroma = boundary(); chroma.chromas[2] = .099;
  expect(checkMeasurements(chroma).failures.map(f => f.rule)).toEqual(["slot_chroma"]);
});
it("passes equality, warns from six to eight and never gates tritan", () => {
  expect(checkMeasurements(boundary()).failures).toEqual([]);
  expect(checkMeasurements(boundary()).warnings.map(w => w.rule)).toEqual(["cvd_distance"]);
  const measurements = boundary(); measurements.pairs[0].protan = 7.99;
  expect(checkMeasurements(measurements).warnings).toHaveLength(1);
  measurements.pairs[0].protan = 8;
  expect(checkMeasurements(measurements).warnings).toEqual([]);
});
it("detects identical adjacent colors and a desaturated slot", () => {
  for (const theme of ["light", "dark"] as const) {
    const duplicate: string[] = [...series[theme]]; duplicate[1] = duplicate[0];
    const failures = checkMeasurements(measurePalette(duplicate, chart[theme].surface)).failures;
    for (const rule of ["normal_distance", "cvd_distance"]) expect(failures.some(f => f.rule === rule && f.a === 0 && f.b === 1)).toBe(true);
    const gray: string[] = [...series[theme]]; gray[2] = "#808080";
    expect(checkMeasurements(measurePalette(gray, chart[theme].surface)).failures.some(f => f.rule === "slot_chroma" && f.slot === 2)).toBe(true);
  }
});
it("matches transfer, full severity matrices and OKLab reference values", () => {
  expect(linearize(.04045)).toBeCloseTo(.00313080495, 10);
  expect(decode("#808080")[0]).toBeCloseTo(.2158605001, 9);
  expect(lab([1, 0, 0])).toEqual(expect.arrayContaining([expect.closeTo(.62795536, 7), expect.closeTo(.22486306, 7), expect.closeTo(.12584630, 7)]));
  expect(lab([1, 1, 1])[0]).toBeCloseTo(1, 7);
  expect(simulate([1, 0, 0], "protan")).toEqual([.152286, .114503, 0]);
  expect(simulate([0, 1, 0], "protan")).toEqual([1, .786281, 0]);
  expect(simulate([1, 0, 0], "deutan")).toEqual([.367322, .280085, 0]);
  expect(simulate([0, 1, 0], "deutan")).toEqual([.860646, .672501, .042940]);
  // Red and a dimmer green remain chromatic and far apart normally, but
  // converge under both full-severity red/green deficiency simulations.
  const red = decode("#ff0000"), green = decode("#006900");
  expect(distance(lab(red), lab(green))).toBeGreaterThan(15);
  expect(distance(lab(simulate(red, "protan")), lab(simulate(green, "protan")))).toBeLessThan(6);
  const deutanGreen = decode("#00a500");
  expect(distance(lab(simulate(red, "deutan")), lab(simulate(deutanGreen, "deutan")))).toBeLessThan(6);
});
it("reports transformed surface contrast and rejects malformed or translucent hex", () => {
  for (const value of ["red", "#fff", "#gg0000", "#ffffffff", "rgba(0,0,0,.5)"]) expect(() => decode(value)).toThrow("opaque six-digit hex");
  expect(() => measurePalette(["#ffffff"], "#000000")).toThrow("six ordered");
  const measured = measurePalette(Array(6).fill("#ffffff"), "#000000");
  expect(measured.surface_contrast).toHaveLength(6);
  expect(measured.surface_contrast[0].normal).toBeCloseTo(21);
  expect(measurePalette(Array(6).fill(chart.dark.surface), chart.dark.surface).surface_contrast[0].deutan).toBe(1);
});

it.each(["light", "dark"] as const)("checks all fifteen slot pairs and both %s chart surfaces", scheme => {
  const colors = series[scheme];
  const distances: number[] = [];
  for (let a = 0; a < colors.length; a++) {
    for (const surface of [surfaces[scheme].ground, surfaces[scheme].raised]) expect(contrastRatio(colors[a], surface)).toBeGreaterThanOrEqual(3);
    for (let b = a + 1; b < colors.length; b++) distances.push(distance(lab(decode(colors[a])), lab(decode(colors[b]))));
  }
  expect(distances).toHaveLength(15);
  // The retained dark blue/violet pair is the closest, at 9.76. Adjacent
  // slots retain the stricter 15 normal / 6 CVD thresholds above.
  expect(Math.min(...distances)).toBeGreaterThanOrEqual(9.7);
  console.log(`${scheme} minimum all-pairs OKLab distance: ${Math.min(...distances).toFixed(2)}`);
});

type LCH = { l: number; c: number; h: number };
function oklch(rgb: Triple): LCH {
  const [l, a, b] = lab(rgb);
  return { l, c: Math.hypot(a, b), h: (Math.atan2(b, a) * 180 / Math.PI + 360) % 360 };
}
const hueGap = (a: number, b: number) => { const delta = Math.abs(a - b) % 360; return Math.min(delta, 360 - delta); };
function slotTwoRoleFailures(candidate: LCH, others: LCH[], statusHues: number[]): string[] {
  const lightness = others.map(color => color.l);
  const failures: string[] = [];
  if (candidate.l < Math.min(...lightness) - .03 || candidate.l > Math.max(...lightness) + .03) failures.push("lightness");
  if (candidate.c < .10) failures.push("chroma");
  if (others.some(color => hueGap(candidate.h, color.h) < 25)) failures.push("slot_hue");
  if (statusHues.some(hue => hueGap(candidate.h, hue) < 30)) failures.push("status_hue");
  return failures;
}
function roleContext(scheme: "light" | "dark") {
  return {
    others: series[scheme].filter((_, slot) => slot !== 1).map(color => oklch(decode(color))),
    statusHues: [bad, warn].map(palette => oklch(decode(palette[scheme === "dark" ? 5 : 7])).h),
  };
}

it.each(["light", "dark"] as const)("keeps slot 2's %s visual weight, chroma and hue role", scheme => {
  const { others, statusHues } = roleContext(scheme);
  expect(slotTwoRoleFailures(oklch(decode(series[scheme][1])), others, statusHues)).toEqual([]);
  for (const surface of [surfaces[scheme].ground, surfaces[scheme].raised]) expect(contrastRatio(series[scheme][1], surface)).toBeGreaterThanOrEqual(3);
});

it.each(["light", "dark"] as const)("rejects former lightness extremes and neutral ink as %s slot 2", scheme => {
  const { others, statusHues } = roleContext(scheme);
  for (const hex of ["#ffffaf", "#000033"]) expect(slotTwoRoleFailures(oklch(decode(hex)), others, statusHues)).toContain("lightness");
  for (const hex of ["#808080", "#111113", "#EDEDEF"]) expect(slotTwoRoleFailures(oklch(decode(hex)), others, statusHues)).toContain("chroma");
});

it("enforces each slot role boundary independently, including circular hue gaps", () => {
  const others = [20, 70, 150, 260, 310].map((h, i) => ({ l: i === 0 ? .5 : .7, c: .15, h }));
  const candidate = { l: .6, c: .15, h: 210 }, statusHues = [20, 70];
  const check = (change: Partial<LCH>) => slotTwoRoleFailures({ ...candidate, ...change }, others, statusHues);
  expect(check({})).toEqual([]);
  expect(check({ l: .5 - .03 })).toEqual([]);
  expect(check({ l: .7 + .03 })).toEqual([]);
  expect(check({ l: .5 - .030001 })).toEqual(["lightness"]);
  expect(check({ l: .7 + .030001 })).toEqual(["lightness"]);
  expect(check({ c: .10 })).toEqual([]);
  expect(check({ c: .099999 })).toEqual(["chroma"]);
  expect(check({ h: 235 })).toEqual([]);
  expect(check({ h: 235.000001 })).toEqual(["slot_hue"]);
  expect(check({ h: 100 })).toEqual([]);
  expect(check({ h: 99.999999 })).toEqual(["status_hue"]);
  expect(check({ h: 359 })).toEqual(["slot_hue", "status_hue"]);
  expect(hueGap(359, 24)).toBe(25);
});

// Exhaustive opaque sRGB search with the role constraints above; minima are
// over slot 2 versus all five retained slots, not just its adjacent slots.
it.each(["light", "dark"] as const)("separates role-constrained slot 2 in every %s vision mode", scheme => {
  const colors = series[scheme].map(decode), candidate = colors[1];
  const minima = modes.map(mode => Math.min(...colors.filter((_, slot) => slot !== 1).map(color =>
    distance(lab(simulate(candidate, mode)), lab(simulate(color, mode))))));
  const floors = scheme === "dark" ? [10.17, 8.89, 8.81, 8.82] : [16.56, 12.84, 14.71, 12.87];
  minima.forEach((minimum, i) => expect(minimum, modes[i]).toBeGreaterThanOrEqual(floors[i]));
  console.log(JSON.stringify({ scheme, slot: 2, oklch: oklch(candidate), minima: Object.fromEntries(modes.map((mode, i) => [mode, minima[i]])) }));
});
