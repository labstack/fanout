import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "../catalog/product";
const meta = { id: "pages-alerts", title: "Pages/Alerts", component: Product, args: { path: "/alerts" }, parameters: { ...pageParameters, } } satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Ready: Story = { play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Alerts" })).toBeVisible();
  await expect((await canvas.findAllByText("Checkout errors", { selector: "p" }))[0]).toBeVisible();
} };
export const Empty: Story = { parameters: { scenario: { empty: true } }, play: async ({ canvas }) => {
  await expect(await canvas.findByText(/No rules yet/)).toBeVisible();
} };
