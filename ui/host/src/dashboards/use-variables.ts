import { useQuery } from "@tanstack/react-query";
import { ALL, type DashboardSpec, type DashboardTime, type VarValue } from "../../../panels/types";
import { panelContent, retryQuery } from "./query-policy";
import { resolveVariables } from "./api";

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
