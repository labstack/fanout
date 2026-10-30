import { createFileRoute } from "@tanstack/react-router";
import { AlertsPage } from "../alerts/page";

export const Route = createFileRoute("/alerts")({
  component: AlertsPage,
});
