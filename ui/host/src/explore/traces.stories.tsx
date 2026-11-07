import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";
import { Product, pageParameters } from "../catalog/product";
const meta = { id: "pages-traces", title: "Pages/Traces", component: Product, args: { path: "/traces" }, parameters: pageParameters } satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Ready: Story = { play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Traces" })).toBeVisible();
  await waitFor(() => expect(canvas.getByRole("button", { name: "Save to dashboard" })).toBeEnabled());
  await expect((await canvas.findAllByText("POST /checkout", { exact: true }))[0]).toBeVisible();
} };
