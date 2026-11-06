import { logRules } from "../catalog/accessibility";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "../catalog/product";
const meta = { id: "pages-logs", title: "Pages/Logs", component: Product, args: { path: "/logs" }, parameters: { ...pageParameters, a11y: { config: { rules: logRules } } } } satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Ready: Story = { play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Logs" })).toBeVisible();
  await expect(await canvas.findByRole("button", { name: "Save to dashboard" })).toBeEnabled();
  await expect(await canvas.findByText("Payment authorization retried after 300 ms", { exact: true })).toBeVisible();
} };
