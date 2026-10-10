import { expect, it } from "vitest";
import { chart, series } from "../../../tokens";

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
