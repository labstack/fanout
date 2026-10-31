/** One builder filter row. The stored form is a filter-language string such as `service = 'checkout'`. */
export type Condition = { field: string; op: Operator; value: string; quoted: boolean };
export const operators = ["=", "!=", ">", ">=", "<", "<="] as const;
type Operator = typeof operators[number];

export const fieldPattern = String.raw`[a-z_][a-z0-9_]*|(?:attributes|resource)\['(?:[^']|'')+'\]`;
const literal = String.raw`'(?:[^']|'')*'|-?\d+(?:\.\d+)?|\$[a-z][a-z0-9_]*`;
const pattern = new RegExp(String.raw`^(${fieldPattern})\s*(!=|>=|<=|=|>|<)\s*(${literal})$`);
const number = /^-?\d+(?:\.\d+)?$/;
const variable = /^\$[a-z][a-z0-9_]*$/;

/** A comparison of one field with one literal, or undefined for anything else, which the builder keeps verbatim. */
export function parseWhere(entry: string): Condition | undefined {
  const match = pattern.exec(entry.trim());
  if (!match) return undefined;
  const [, field, op, raw] = match;
  const quoted = raw.startsWith("'");
  return { field, op: op as Operator, value: quoted ? raw.slice(1, -1).replaceAll("''", "'") : raw, quoted };
}

/** Prints the literal as typed: a quoted value stays a string, so a typed attribute comparison keeps its type. A dashboard variable such as `$service` is never quoted. */
export function printWhere(condition: Condition, variables: readonly string[] = []): string {
  const trimmed = condition.value.trim();
  if (variable.test(trimmed) && variables.includes(trimmed.slice(1))) return `${condition.field} ${condition.op} ${trimmed}`;
  const quoted = condition.quoted || !number.test(trimmed);
  const value = quoted ? `'${condition.value.replaceAll("'", "''")}'` : condition.value.trim();
  return `${condition.field} ${condition.op} ${value}`;
}

// FanQL's duration units, in milliseconds.
const units: Record<string, number> = { h: 3_600_000, m: 60_000, s: 1000, ms: 1, us: 0.001 };
const durationPattern = /^(\d+(?:\.\d+)?)\s*(h|m|s|ms|us)$/i;
// Products such as 1.1 * 1000 carry binary noise; twelve digits is far finer than any threshold.
const exact = (n: number) => Number(n.toPrecision(12));

/** Milliseconds for a duration such as `100ms` or `1.5s`; a bare number is already milliseconds. */
export function durationMs(text: string): number | undefined {
  const trimmed = text.trim();
  if (number.test(trimmed)) return Number(trimmed);
  const match = durationPattern.exec(trimmed);
  return match ? exact(Number(match[1]) * units[match[2].toLowerCase()]) : undefined;
}

/** Milliseconds in the largest unit that keeps them exact, as FanQL prints a duration. */
export function durationText(ms: number): string {
  for (const [unit, size] of Object.entries(units)) {
    if (ms >= size && exact(exact(ms / size) * size) === ms) return `${exact(ms / size)}${unit}`;
  }
  return `${ms}ms`;
}
