import { panelResultsForDashboard } from "./use-panel-results";
import { variableOptionsForScope } from "./use-variables";

// Annotations belong to the panel batch. Each hook owns its key matching.
export function dashboardDataPredicate(id: string) {
  return (query: { queryKey: readonly unknown[] }) => panelResultsForDashboard(query, id) || variableOptionsForScope(query, `dashboard-${id}`);
}
