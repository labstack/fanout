import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Product, pageParameters } from "./product";
import { railRules } from "./accessibility";
import { analysisReady, sendQuestion, threadInRail } from "./interactions";
import { question } from "./fixtures";
const meta = {
  id: "flows-ask-a-question", title: "Flows/Ask a question", component: Product,
  parameters: { ...pageParameters, a11y: { config: { rules: railRules } }, scenario: { run: "question", empty: true }, docs: { ...pageParameters.docs, description: { component: "Ask about the commerce fleet, receive a streamed answer, and review the real health app inline. Primary recording size: 1280 × 800." } } },
} satisfies Meta<typeof Product>;
export default meta;
type Story = StoryObj<typeof meta>;
export const ReadyToAsk: Story = { play: async ({ canvas }) => { await expect(await canvas.findByRole("heading", { name: "What do you want to know about your system?" })).toBeVisible(); } };
export const AskAndReview: Story = { play: async context => {
  await sendQuestion(context, question);
  await context.step("Read the streamed answer", async () => {
    await expect(await context.canvas.findByText(/Checkout needs attention:/)).toBeVisible();
    await expect(await context.canvas.findByRole("button", { name: "Send message" }, { timeout: 30_000 })).toBeVisible();
  });
  await analysisReady(context);
  await threadInRail(context, question);
} };
export const Answered: Story = { args: { path: "/chat/commerce-review" }, parameters: { scenario: { chat: "answered" } }, play: async context => {
  await analysisReady(context);
  await threadInRail(context, question);
} };
