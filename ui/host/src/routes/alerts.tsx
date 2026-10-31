import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { AlertsPage } from "../alerts/page";
import { defaultRange, toExploreParams } from "../explore/search";

export const Route = createFileRoute("/alerts")({
  component: Alerts,
});

function Alerts() {
  const navigate = useNavigate();
  return <AlertsPage onExplore={q => void navigate({ to: "/explore", search: toExploreParams({ q, range: defaultRange, tab: "query" }) })} />;
}
