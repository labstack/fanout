import { createFileRoute } from "@tanstack/react-router";
import { IngestSettings } from "../settings";

export const Route = createFileRoute("/settings_/ingest")({
  component: IngestSettings,
});
