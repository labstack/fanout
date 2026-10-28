import { nestedMainRules, healthRules } from "../catalog/accessibility";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "../catalog/product";

const meta = {
  id: "pages-dashboards", title: "Pages/Dashboards", component: Product,
  args: { path: "/dashboards" },
  parameters: { ...pageParameters, a11y: { config: { rules: [...nestedMainRules, ...healthRules] } }, docs: { ...pageParameters.docs, description: { component: "The index opens the default dashboard when one exists. An empty workspace offers Create with AI." } } },
} satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const NoDashboards: Story = { parameters: { scenario: { empty: true } }, play: async ({ canvas }) => { await expect(await canvas.findByRole("heading", { name: "No dashboards yet" })).toBeVisible(); } };
export const OpensDefault: Story = { play: async ({ canvas }) => { await expect(await canvas.findByRole("region", { name: "Service health: degraded; 4 services" })).toBeVisible(); } };
