import { Box } from "@mantine/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";
import { panels, results } from "../catalog/fixtures";
import { PanelCard } from "./panel-card";

import { healthRules, loadingRules } from "../catalog/accessibility";

const meta = {
  id: "components-dashboard-panels", title: "Components/Dashboard panels", component: PanelCard,
  args: { panel: panels[0], title: panels[0].title, result: results[0], height: 440, loading: false, group: "catalog", editing: false, agentAvailable: false, onSelect: fn(), onRetry: fn() },
  decorators: [(Story, context) => <Box h={context.args.height}><Story /></Box>],
  parameters: { docs: { description: { component: "All fifteen shipping visualizations. The host and embedded app share these panel cards and the package-free panel model." } } },
} satisfies Meta<typeof PanelCard>;
export default meta;
type Story = StoryObj<typeof meta>;
const panel = (id: string): Story => { const p = panels.find(p => p.id === id)!; return { args: { panel: p, title: p.title, result: results.find(r => r.id === id) } }; };
export const Requests: Story = panel("requests");
export const Latency: Story = panel("latency");
export const Health: Story = { ...panel("health"), parameters: { a11y: { config: { rules: healthRules } } }, play: async ({ canvas, userEvent, args }) => {
  await userEvent.click(await canvas.findByRole("button", { name: /checkout/ }));
  await expect(args.onSelect).toHaveBeenCalledWith("checkout");
} };
export const Traffic: Story = panel("traffic");
export const ErrorRate: Story = panel("errors");
export const ServicePerformance: Story = panel("services");
export const OperationsNotes: Story = panel("notes");
export const LatencyDensity: Story = panel("density");
export const LatencyDistribution: Story = panel("distribution");
export const TrafficAndLatency: Story = panel("correlation");
export const ServiceTimeline: Story = panel("timeline");
export const Logs: Story = panel("logs");
export const LogPatterns: Story = panel("patterns");
export const Traces: Story = panel("traces");
export const ServiceDependencies: Story = panel("topology");
export const Loading: Story = { args: { result: undefined }, parameters: { a11y: { config: { rules: loadingRules } } } };
export const Empty: Story = { args: { result: { id: "requests", status: "empty", diagnosis: "No requests in this time range.", elapsed_ms: 18 } } };
export const Error: Story = { args: { result: { id: "requests", status: "error", error: "The telemetry query timed out. Try a shorter time range.", elapsed_ms: 3000 } } };
export const Data: Story = { play: async ({ canvas, userEvent }) => {
  await userEvent.click(canvas.getByRole("button", { name: /^Data$/ }));
  await expect(await canvas.findByRole("table")).toBeVisible();
} };
