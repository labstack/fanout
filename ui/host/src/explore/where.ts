/** One builder filter row. The stored form is a filter-language string such as `service = 'checkout'`. */
export type Condition = { field: string; op: Operator; value: string; quoted: boolean };
export const operators = ["=", "!=", ">", ">=", "<", "<="] as const;
type Operator = typeof operators[number];

export const fieldPattern = String.raw`[a-z_][a-z0-9_]*|(?:attributes|resource)\['(?:[^']|'')+'\]`;
const literal = String.raw`'(?:[^']|'')*'|-?\d+(?:\.\d+)?`;
const pattern = new RegExp(String.raw`^(${fieldPattern})\s*(!=|>=|<=|=|>|<)\s*(${literal})$`);
const number = /^-?\d+(?:\.\d+)?$/;

/** A comparison of one field with one literal, or undefined for anything else, which the builder keeps verbatim. */
export function parseWhere(entry: string): Condition | undefined {
  const match = pattern.exec(entry.trim());
  if (!match) return undefined;
  const [, field, op, raw] = match;
  const quoted = raw.startsWith("'");
  return { field, op: op as Operator, value: quoted ? raw.slice(1, -1).replaceAll("''", "'") : raw, quoted };
}

/** Prints the literal as typed: a quoted value stays a string, so a typed attribute comparison keeps its type. */
export function printWhere(condition: Condition): string {
  const quoted = condition.quoted || !number.test(condition.value.trim());
  const value = quoted ? `'${condition.value.replaceAll("'", "''")}'` : condition.value.trim();
  return `${condition.field} ${condition.op} ${value}`;
}
