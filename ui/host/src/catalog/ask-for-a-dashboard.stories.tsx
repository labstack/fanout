import { nestedMainRules, healthAndRailRules } from "./accessibility";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "./product";
import { dashboardInRail, dashboardRows, sendQuestion, threadInRail } from "./interactions";
import { dashboardQuestion } from "./fixtures";
const meta = {
  id: "flows-ask-chat-for-a-dashboard", title: "Flows/Ask chat for a dashboard", component: Product,
  parameters: { ...pageParameters, a11y: { config: { rules: [...nestedMainRules, ...healthAndRailRules] } }, scenario: { empty: true, run: "dashboard" }, docs: { ...pageParameters.docs, description: { component: "Create a saved commerce dashboard from a streamed tool receipt, then open it through the real local router. Primary recording size: 1280 × 800." } } },
} satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const ReadyToRequest: Story = { play: async ({ canvas }) => { await expect(await canvas.findByRole("heading", { name: "What do you want to know about your system?" })).toBeVisible(); } };
export const Saved: Story = { args: { path: "/chat/commerce-review" }, parameters: { scenario: { chat: "saved" } }, play: async context => {
  await expect(await context.canvas.findByText(/Saved v1/)).toBeVisible();
  await threadInRail(context, dashboardQuestion);
  await dashboardInRail(context, dashboardQuestion);
} };
export const AskSaveAndOpen: Story = { play: async context => {
  await sendQuestion(context, dashboardQuestion);
  await context.step("Wait for the committed save receipt", async () => {
    await expect(await context.canvas.findByText(/Saved v1/)).toBeVisible();
    await expect(await context.canvas.findByRole("button", { name: "Send message" }, { timeout: 30_000 })).toBeVisible();
  });
  await threadInRail(context, dashboardQuestion);
  await dashboardInRail(context, dashboardQuestion);
  await context.step("Open the saved dashboard", async () => {
    await context.userEvent.click(context.canvas.getByRole("link", { name: "Open dashboard" }));
    await expect(await context.canvas.findByRole("heading", { name: "Commerce health" })).toBeVisible();
    await expect(await context.canvas.findByRole("region", { name: "Service health: degraded; 4 services" })).toBeVisible();
  });
  await dashboardRows(context);
  await dashboardInRail(context, dashboardQuestion);
} };
