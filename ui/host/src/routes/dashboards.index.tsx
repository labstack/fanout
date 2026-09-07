import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useFanoutApp } from "../app-context";
import Dashboard from "../dashboard";
import { dashboardSearch, type DashboardSearch } from "../dashboard-search";

export const Route = createFileRoute("/dashboards/")({
  component: DashboardIndex,
  validateSearch: dashboardSearch,
});

function DashboardIndex() {
  const search = Route.useSearch();
  const navigate = useNavigate();
  const { agentAvailable, openChat } = useFanoutApp();
  return <Dashboard
    agentAvailable={agentAvailable}
    onOpenChat={openChat}
    onDashboardChange={(dashboardId) => void navigate({ to: "/dashboards/$dashboardId", params: { dashboardId }, search: search as DashboardSearch, replace: true })}
    urlFilters={search}
  />;
}
