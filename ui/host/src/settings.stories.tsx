import { nestedMainRules, settingsLinkRules } from "./catalog/accessibility";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "./catalog/product";
const meta = { id: "pages-settings", title: "Pages/Settings", component: Product, args: { path: "/settings" }, parameters: { ...pageParameters, docs: { ...pageParameters.docs, description: { component: "The shipping telemetry connection screen, with a fictional TLS endpoint, ingest-token status, and connected agent." } }, a11y: { config: { rules: [...nestedMainRules, ...settingsLinkRules] } } } } satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Connected: Story = { play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Connect telemetry" })).toBeVisible();
  await expect(await canvas.findByText("Operations assistant")).toBeVisible();
} };
