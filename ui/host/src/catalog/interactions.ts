import type { StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { Product } from "./product";

type Context = Parameters<NonNullable<StoryObj<typeof Product>["play"]>>[0];

export async function sendQuestion({ canvas, userEvent, step, globals, parameters }: Context, question: string) {
  await step("Ask Fanout", async () => {
    const composer = await canvas.findByRole("textbox", { name: "Message Fanout" });
    await waitFor(() => expect(composer).toBeEnabled());
    if (globals.pace === "recording" || parameters.scenario?.pace === "recording") {
      for (const character of question) {
        await userEvent.type(composer, character);
        await new Promise(resolve => setTimeout(resolve, 45));
      }
    } else await userEvent.type(composer, question);
    await userEvent.click(canvas.getByRole("button", { name: "Send message" }));
  });
}

export async function threadInRail({ canvas, step }: Context, question: string) {
  await step("Find the conversation in the navigation", async () => {
    const rail = within(await canvas.findByRole("navigation", { name: "Navigation" }));
    const prefix = question.slice(0, 24).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const thread = await rail.findByRole("button", { name: new RegExp(`^${prefix}`) });
    await expect(thread).toBeVisible();
    await expect(thread.querySelector('[data-size="xs"]')).toHaveTextContent(/\d+:\d+/);
    await expect(rail.queryByText("No chats yet")).not.toBeInTheDocument();
  });
}

export async function dashboardInRail({ canvas, step }: Context, question: string) {
  await step("Find the saved dashboard and its originating request", async () => {
    const rail = within(await canvas.findByRole("navigation", { name: "Navigation" }));
    await expect(await rail.findByRole("link", { name: /^Commerce health/ })).toBeVisible();
    await expect(await rail.findByRole("link", { name: `Built from: ${question}` })).toBeVisible();
    await expect(rail.queryByRole("button", { name: "New dashboard" })).not.toBeInTheDocument();
  });
}

export async function analysisReady({ canvasElement, step }: Context) {
  await step("Render the real sandboxed analysis app", async () => {
    await waitFor(() => {
      const iframe = canvasElement.querySelector<HTMLIFrameElement>('iframe[title="Fanout analysis view"]');
      expect(iframe).toBeVisible();
      expect(iframe?.getAttribute("sandbox")).toBe("allow-scripts");
      // The built app reports its rendered content height over AppBridge.
      expect(iframe?.getBoundingClientRect().height).toBeGreaterThan(240);
    });
  });
}

export async function dashboardRows({ canvasElement, step }: Context) {
  if (window.innerWidth < 1280) return;
  await step("Keep desktop panels beside one another", async () => {
    await waitFor(() => {
      const requests = canvasElement.querySelector<HTMLElement>('.react-grid-item[data-panel="requests"]');
      const latency = canvasElement.querySelector<HTMLElement>('.react-grid-item[data-panel="latency"]');
      expect(requests).toBeVisible();
      expect(latency).toBeVisible();
      const first = requests!.getBoundingClientRect();
      const second = latency!.getBoundingClientRect();
      expect(Math.abs(first.top - second.top), "Requests and latency share a row").toBeLessThan(2);
      expect(second.left, "Latency sits to the right of requests").toBeGreaterThanOrEqual(first.right);
    });
  });
}
