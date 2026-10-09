import { useQuery } from "@tanstack/react-query";
import { ALL, type DashboardSpec, type DashboardTime, type VarValue } from "../../../panels/types";
import { variableOptionsKey } from "./query-keys";
import type { QueryBody } from "./api";
export type VariableOptions = Record<string, { value: string; count?: number }[]>;
export type VariableResolver = (body: Omit<QueryBody, "panels" | "widths" | "compare">, signal?: AbortSignal) => Promise<VariableOptions>;

export function variableOptionsForScope(query: { queryKey: readonly unknown[] }, scope: string): boolean {
  return query.queryKey[0] === "variables" && query.queryKey[1] === scope;
}

export function useVariableOptions(scope: string, spec: DashboardSpec, time: DashboardTime, vars: Record<string, VarValue>, resolveVariables: VariableResolver, retry: false | ((count: number, error: Error) => boolean) = false) {
  const hasVariables = Boolean(spec.variables?.some(v => v.kind === "query"));
  const query = useQuery({
    queryKey: variableOptionsKey(scope, spec, time, vars),
    queryFn: async ({ signal }) => {
      const options = await resolveVariables({ dashboard: spec, time, vars }, signal);
      return Object.fromEntries((spec.variables ?? []).map(variable => [variable.name, options[variable.name] ?? (variable.kind === "custom" ? variable.options?.map(value => ({ value })) : undefined) ?? []]));
    },
    enabled: hasVariables,
    retry,
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
