/** One builder filter row. The stored form is a filter-language string such as `service = 'checkout'`. */
export type Condition = { field: string; op: Operator; value: string };
export const operators = ["=", "!=", ">", ">=", "<", "<="] as const;
export type Operator = typeof operators[number];

const field = String.raw`[a-z_][a-z0-9_]*|(?:attributes|resource)\['(?:[^']|'')+'\]`;
const literal = String.raw`'(?:[^']|'')*'|-?\d+(?:\.\d+)?`;
const pattern = new RegExp(String.raw`^(${field})\s*(!=|>=|<=|=|>|<)\s*(${literal})$`);

/** A comparison of one field with one literal, or undefined for anything else, which the builder keeps verbatim. */
export function parseWhere(entry: string): Condition | undefined {
  const match = pattern.exec(entry.trim());
  if (!match) return undefined;
  const [, name, op, raw] = match;
  const value = raw.startsWith("'") ? raw.slice(1, -1).replaceAll("''", "'") : raw;
  return { field: name, op: op as Operator, value };
}

export function printWhere(condition: Condition, numeric: boolean): string {
  const value = numeric && /^-?\d+(?:\.\d+)?$/.test(condition.value.trim()) ? condition.value.trim() : `'${condition.value.replaceAll("'", "''")}'`;
  return `${condition.field} ${condition.op} ${value}`;
}
