import { useQuery } from "@tanstack/react-query";
import { ALL, type DashboardSpec, type DashboardTime, type Variable, type VarValue } from "../../../panels/types";
import { panelContent, retryQuery } from "./query-policy";
import { resolveVariables } from "./api";

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

export function useVariableOptions(dashboardId: string, _version: number, spec: DashboardSpec, time: DashboardTime, vars: Record<string, VarValue>) {
  const hasVariables = (spec.variables?.length ?? 0) > 0;
  const query = useQuery({
    queryKey: ["variables", dashboardId, panelContent(spec), time, vars],
    queryFn: async ({ signal }) => {
      const options = await resolveVariables({ dashboard: spec, time, vars }, signal);
      return Object.fromEntries((spec.variables ?? []).map(variable => [variable.name, options[variable.name] ?? []]));
    },
    enabled: hasVariables,
    retry: retryQuery,
    staleTime: 60_000,
    placeholderData: (previous) => previous,
  });
  // Match values(): only a query without a usable provided value, default
  // or All requires options before the first panel request.
  const currentData = query.isPlaceholderData ? undefined : query.data;
  const ready = query.isError || (spec.variables ?? []).every((variable) =>
    variable.kind !== "query" ||
    (vars[variable.name] !== undefined && vars[variable.name] !== ALL && (variable.multi || !Array.isArray(vars[variable.name]) || vars[variable.name].length > 0)) ||
    Boolean(variable.default) || variable.include_all || currentData?.[variable.name] !== undefined);
  return { ...query, currentData, ready };
}
