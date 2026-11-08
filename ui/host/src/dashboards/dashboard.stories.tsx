import { assertLoadingState } from "../catalog/loading";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "../catalog/product";
import { dashboardRows } from "../catalog/interactions";

const meta = {
  id: "pages-dashboard", title: "Pages/Dashboard", component: Product,
  args: { path: "/dashboards/commerce-health" },
  parameters: { ...pageParameters, docs: { ...pageParameters.docs, description: { component: "A saved dashboard rendered by the real route, query client, toolbar, grid, and panels." } } },
} satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Ready: Story = { play: async context => {
  const { canvas } = context;
  await expect(await canvas.findByRole("heading", { name: "Commerce health" })).toBeVisible();
  await expect(await canvas.findByRole("region", { name: "Service health: degraded; 4 services" })).toBeVisible();
  await dashboardRows(context);
} };
export const Loading: Story = { parameters: { scenario: { dashboard: "loading" } }, play: async ({ canvas }) => {
  const placeholder = await assertLoadingState(await canvas.findByText("Loading dashboard"), "Loading dashboard");
  await expect(placeholder.querySelector('[data-dashboard-header-skeleton]')).toBeVisible();
  const grid = placeholder.querySelector<HTMLElement>('.dashboard-loading-grid')!;
  await expect(grid).toBeVisible();
  await expect(getComputedStyle(grid).gridTemplateColumns.split(" ")).toHaveLength(12);
  await expect(grid.querySelectorAll('[data-skeleton-shape="stat"]')).toHaveLength(3);
  await expect(grid.querySelector('[data-skeleton-shape="chart"]')).toBeVisible();
  await expect(grid.querySelector('[data-skeleton-shape="table"]')).toBeVisible();
} };
export const Empty: Story = { parameters: { scenario: { dashboard: "empty" } }, play: async ({ canvas }) => { await expect(await canvas.findByRole("heading", { name: "Commerce health" })).toBeVisible(); } };
export const Error: Story = { parameters: { scenario: { dashboard: "error" } }, play: async ({ canvas }) => { await expect(await canvas.findByRole("heading", { name: "This dashboard isn't here" })).toBeVisible(); } };

export const InvalidPanel: Story = { parameters: { scenario: { dashboard: "invalid" } }, play: async ({ canvas }) => {
  await expect(await canvas.findByText("Unknown field: request_total")).toBeVisible();
  await expect(await canvas.findByRole("region", { name: "Service health: degraded; 4 services" })).toBeVisible();
} };
