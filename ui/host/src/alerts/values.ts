/** Rollup variables a rule can read, as internal/alert/types.go Env names them. */
const names = /\b(error_rate|p50|p95|throughput|log_count|z_score|health_score)(_delta)?\b/g;

/** The variables an expression reads, in order, outside string literals. */
export function variables(expression: string): string[] {
  const code = expression.replace(/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/g, "\"\"");
  return [...new Set(Array.from(code.matchAll(names), m => m[0]))];
}

const percent = new Intl.NumberFormat("en-US", { style: "percent", maximumFractionDigits: 2 });
const count = new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 });
const decimal = new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 });

/** A variable's value in its unit: error rate as a percent, latency in ms, deltas as signed percent changes. */
export function formatValue(variable: string | undefined, value: number): string {
  if (variable === "error_rate") return percent.format(value);
  if (variable === "p50" || variable === "p95") return `${decimal.format(value)} ms`;
  if (variable === "throughput" || variable === "log_count") return count.format(value);
  if (variable?.endsWith("_delta")) return `${value > 0 ? "+" : ""}${decimal.format(value)}%`;
  return decimal.format(value);
}

/** Warns when an expression compares error_rate with a number above 1, which reads like a percent. */
export function percentMistake(expression: string): string | undefined {
  const code = expression.replace(/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/g, "\"\"");
  const match = /\berror_rate\s*[<>=!]=?\s*(\d+(?:\.\d+)?)/.exec(code) ?? /(\d+(?:\.\d+)?)\s*[<>=!]=?\s*error_rate\b/.exec(code);
  const value = match ? Number(match[1]) : 0;
  return value > 1 ? `error_rate runs from 0 to 1, so ${match![1]} can never be reached. For ${match![1]}%, write ${decimal.format(value / 100)}.` : undefined;
}
