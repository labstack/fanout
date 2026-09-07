import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useFanoutApp } from "../app-context";
import Dashboard from "../dashboard";
import { dashboardSearch, type DashboardSearch } from "../dashboard-search";

export const Route = createFileRoute("/dashboards/$dashboardId")({
  component: DashboardDetail,
  validateSearch: dashboardSearch,
});

function DashboardDetail() {
  const { dashboardId } = Route.useParams();
  const search = Route.useSearch();
  const navigate = useNavigate();
  const { agentAvailable, openChat } = useFanoutApp();
  return <Dashboard
    dashboardID={dashboardId}
    agentAvailable={agentAvailable}
    onOpenChat={openChat}
    onDashboardChange={(nextID, replace) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: nextID }, search: search as DashboardSearch, replace })}
    urlFilters={search}
    onFiltersChange={(filters) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId }, search: { window: filters.window, namespace: filters.namespace || undefined }, replace: true })}
  />;
}
