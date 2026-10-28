import { railRules } from "./catalog/accessibility";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "./catalog/product";
import { analysisReady, sendQuestion } from "./catalog/interactions";
import { question } from "./catalog/fixtures";

const meta = {
  id: "pages-chat", title: "Pages/Chat", component: Product,
  parameters: { ...pageParameters, a11y: { config: { rules: railRules } }, docs: { ...pageParameters.docs, description: { component: "The shipping chat screen and navigation, with fictional API and streamed telemetry responses." } } },
} satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
  parameters: { scenario: { empty: true } },
  play: async ({ canvas }) => { await expect(await canvas.findByRole("heading", { name: "What do you want to know about your system?" })).toBeVisible(); },
};
export const Running: Story = {
  parameters: { scenario: { run: "running" } },
  play: async context => {
    await sendQuestion(context, question);
    await expect(await context.canvas.findByText("Checking system health…")).toBeVisible();
    await expect(context.canvas.getByRole("button", { name: "Stop" })).toBeEnabled();
  },
};
export const AnsweredWithAView: Story = {
  args: { path: "/chat/commerce-review" }, parameters: { scenario: { chat: "answered" } },
  play: async context => {
    await expect(await context.canvas.findByText(/Checkout needs attention:/)).toBeVisible();
    await analysisReady(context);
  },
};
export const Error: Story = {
  args: { path: "/chat/commerce-review" }, parameters: { scenario: { chat: "error" } },
  play: async ({ canvas }) => { await expect(await canvas.findByText("This chat could not be restored. Start a new chat or try again.")).toBeVisible(); },
};
export const ProviderError: Story = {
  parameters: { scenario: { run: "error" } },
  play: async context => {
    await sendQuestion(context, question);
    await expect(await context.canvas.findByRole("button", { name: "Retry" })).toBeVisible();
  },
};
