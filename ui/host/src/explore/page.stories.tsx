import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";
import { Product, pageParameters } from "../catalog/product";
import { exploreQuery, exploreTypo } from "../catalog/fixtures";

const path = (q: string) => `/explore?q=${encodeURIComponent(q)}`;
const meta = {
  id: "pages-explore", title: "Pages/Explore", component: Product,
  args: { path: path(exploreQuery) }, parameters: pageParameters,
} satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Ready: Story = { play: async ({ canvas }) => {
  await expect(await canvas.findByRole("heading", { name: "Explore" })).toBeVisible();
  await waitFor(() => expect(canvas.getByRole("button", { name: "Save to dashboard" })).toHaveAttribute("aria-disabled", "false"));
} };
export const Empty: Story = { args: { path: "/explore" } };
export const Builder: Story = { args: { path: `${path(exploreQuery)}&tab=builder` } };
export const Diagnostics: Story = { args: { path: path(exploreTypo) }, play: async ({ canvas, userEvent }) => {
  const editor = await canvas.findByRole("textbox", { name: "FanQL query" });
  await expect(await canvas.findByRole("alert")).toHaveTextContent("Unknown measure: countt");
  await expect(editor).toHaveAttribute("aria-invalid", "true");
  await userEvent.click(canvas.getByRole("button", { name: "Replace with count" }));
  await expect(editor).toHaveTextContent(exploreQuery);
  await expect(editor).toHaveAttribute("aria-invalid", "false");
} };
export const InvalidPanel: Story = { parameters: { scenario: { explore: "invalid" } }, play: async ({ canvas }) => {
  await expect(await canvas.findByRole("alert")).toHaveTextContent("Unknown field: request_total");
  await waitFor(() => expect(canvas.getByRole("textbox", { name: "FanQL query" })).toHaveAttribute("aria-invalid", "true"));
  await expect(canvas.getByRole("button", { name: "Save to dashboard" })).toHaveAttribute("aria-disabled", "true");
} };
export const SqlDiagnostics: Story = { args: { path: "/explore?tab=sql&sql=SELECT+request_total+FROM+spans" }, parameters: { scenario: { explore: "invalid" } }, play: async ({ canvas }) => {
  await expect(await canvas.findByRole("alert")).toHaveTextContent("Unknown field: request_total");
  await expect(canvas.getByRole("button", { name: "Save to dashboard" })).toHaveAttribute("aria-disabled", "true");
} };
