import { ALL, type Variable, type VarValue } from "./types";

/** internal/panel/variables.go chooseValue precedence, with stale URL choices
 * filtered against options once available. Text values are free-form. */
export function currentValue(variable: Variable, vars: Record<string, VarValue>, options?: { value: string }[] | null): VarValue {
  if (variable.kind === "constant") return variable.value ?? "";
  const opts = variable.kind === "custom" ? variable.options?.map((value) => ({ value })) ?? [] : options === null ? [] : options;
  const supplied = vars[variable.name];
  const given = !variable.multi && Array.isArray(supplied) && supplied.length === 0 ? undefined : supplied;
  if (given === ALL) {
    if (variable.include_all) return ALL;
  } else if (given !== undefined) {
    if (variable.kind === "text" || opts === undefined) return given;
    const allowed = new Set(opts.map((option) => option.value));
    if (Array.isArray(given)) {
      if (given.length === 0 && variable.multi) return [];
      const valid = given.filter((value) => allowed.has(value));
      if (valid.length > 0) return valid;
    } else if (allowed.has(given)) return given;
  }
  if (variable.default) return variable.default;
  if (variable.include_all) return ALL;
  return opts?.[0]?.value ?? "";
}


export const interpolate = (text: string, vars: Record<string, VarValue>) =>
  text.replace(/\$([a-z][a-z0-9_]*)/g, (match, name: string) => { const v = vars[name]; return v === undefined ? match : v === "$__all" ? "all" : Array.isArray(v) ? v.join(", ") : v; });

