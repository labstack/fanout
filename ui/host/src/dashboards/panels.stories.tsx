import { assertLoadingState } from "../catalog/loading";
import { Box } from "@mantine/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";
import { panels, results, invalidResult } from "../catalog/fixtures";
import { PanelCard } from "./panel-card";


const meta = {
  id: "components-dashboard-panels", title: "Components/Dashboard panels", component: PanelCard,
  args: { panel: panels[0], title: panels[0].title, result: results[0], height: 440, loading: false, group: "catalog", editing: false, agentAvailable: false, onSelect: fn(), onRetry: fn(), onZoom: fn() },
  decorators: [(Story, context) => <Box h={context.args.height}><Story /></Box>],
  parameters: { docs: { description: { component: "All fifteen shipping visualizations. The host and embedded app share these panel cards and the package-free panel model." } } },
} satisfies Meta<typeof PanelCard>;
export default meta;
type Story = StoryObj<typeof meta>;
const panel = (id: string): Story => { const p = panels.find(p => p.id === id)!; return { args: { panel: p, title: p.title, result: results.find(r => r.id === id) } }; };
export const Requests: Story = panel("requests");
export const Latency: Story = panel("latency");
export const Health: Story = { ...panel("health"), play: async ({ canvas, userEvent, args }) => {
  const checkout = await canvas.findByRole("button", { name: /checkout/ });
  await userEvent.click(checkout);
  await expect(args.onSelect).toHaveBeenCalledWith("checkout");
  await userEvent.click(canvas.getByText("System health", { exact: true }));
  await expect(checkout).not.toHaveFocus();
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
const loading = (id: string, shape: string): Story => ({
  args: { ...panel(id).args, result: undefined, loading: true },
  play: async ({ canvas, canvasElement, args }) => {
    await assertLoadingState(await canvas.findByText(`Loading panel ${args.title}`), `Loading panel ${args.title}`, shape);
    const title = canvas.getByText(args.title, { exact: true });
    await expect(title).toBeVisible();
    await expect(title.closest('[data-panel-body]')).toBeNull();
    await expect(canvasElement.querySelector('[data-panel-body] [data-skeleton-shape]')).toHaveAttribute("data-skeleton-shape", shape);
    if (shape === "chart") await expect(canvasElement.querySelector('[data-skeleton-axis]')).toBeVisible();
    if (shape === "table") {
      await expect(canvasElement.querySelector('[data-skeleton-row="header"]')).toBeVisible();
      await expect(canvasElement.querySelectorAll('[data-skeleton-row="body"]')).toHaveLength(5);
    }
  },
});
export const Loading: Story = loading("requests", "stat");
export const LoadingChart: Story = loading("latency", "chart");
export const LoadingTable: Story = loading("services", "table");
export const LoadingGauge: Story = loading("errors", "gauge");
export const LoadingHeatmap: Story = loading("density", "heatmap");
export const LoadingText: Story = loading("notes", "text");
export const LoadingLogs: Story = loading("logs", "table");
export const Empty: Story = { args: { result: { id: "requests", status: "empty", diagnosis: "No requests in this time range.", elapsed_ms: 18 } } };
export const Error: Story = { args: { result: { id: "requests", status: "error", error: "The telemetry query timed out. Try a shorter time range.", elapsed_ms: 3000, request_error: ["requests"], retryable: true } } };
export const Data: Story = { args: { initialView: "Data" }, play: async ({ canvas, userEvent }) => {
  await expect(await canvas.findByRole("table")).toBeVisible();
  await userEvent.click(canvas.getByRole("button", { name: /^Spec$/ }));
  await userEvent.click(canvas.getByRole("button", { name: /^Data$/ }));
  await expect(await canvas.findByRole("table")).toBeVisible();
} };

export const Invalid: Story = { args: { result: invalidResult }, play: async ({ canvas }) => {
  await expect(canvas.getByText("Unknown field: request_total", { exact: true })).toBeVisible();
  await expect(canvas.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
} };
export const InvalidDataTab: Story = { args: { result: invalidResult, initialView: "Data" }, play: async ({ canvas, userEvent, args }) => {
  await expect(canvas.getByText("Unknown field: request_total", { exact: true })).toBeVisible();
  await expect(canvas.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
  await userEvent.click(canvas.getByRole("button", { name: /^Data$/ }));
  await expect(args.onRetry).not.toHaveBeenCalled();
} };

export const HeatmapResolution: Story = { ...panel("density"), args: { ...panel("density").args, result: {id: "density", status: "ok", elapsed_ms: 24, interval: "1m", from_ms: 0, to_ms: 4200000, frame: {columns: [{name:"time",type:"time",role:"time"},{name:"bucket_lower",type:"number",role:"dimension",unit:"ms"},{name:"bucket_upper",type:"number",role:"dimension",unit:"ms"},{name:"count",type:"number",role:"measure",unit:"count"}], values: [Array.from({length:70},(_,i)=>i*60000),Array(70).fill(0),Array(70).fill(100),Array.from({length:70},(_,i)=>i+1)],rows:70}} } };
export const TraceIds: Story = { ...panel("traces"), play: async ({canvas}) => {
  const ids=canvas.getAllByTitle(/^[a-f0-9]{32}$/);
  await expect(ids[0].textContent).toMatch(/…$/);
  await expect(canvas.queryByRole("button", {name: /^Chart$/})).not.toBeInTheDocument();
} };

export const SelectedLog: Story = { ...panel("logs"), args: { ...panel("logs").args, controls: {selected: 0} } };
