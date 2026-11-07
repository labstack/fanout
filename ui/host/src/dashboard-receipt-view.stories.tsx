import { Box } from "@mantine/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";
import { savedReceipt, invalidReceipt, correctedReceipt, savedInvalidReceipt } from "./catalog/fixtures";
import { RouterFrame } from "./catalog/router-frame";
import { DashboardReceiptView } from "./dashboard-receipt-view";
const meta = {
  id: "components-dashboard-receipt", title: "Components/Dashboard receipt", component: DashboardReceiptView,
  args: { receipt: savedReceipt }, decorators: [Story => <RouterFrame><Box maw={640} mx="auto"><Story /></Box></RouterFrame>],
} satisfies Meta<typeof DashboardReceiptView>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Saved: Story = {};
export const Details: Story = { args: { initialExpanded: true }, play: async ({ canvas, userEvent }) => {
  await expect(canvas.getByText(/Save check: checked/)).toBeVisible();
} };
export const Building: Story = { args: { receipt: { ...savedReceipt, saved: undefined, stages: { ...savedReceipt.stages, save: { state: "incomplete" } } }, running: true, awaitingAnswer: true, activity: "Building your dashboard…" } };

export const InvalidPanel: Story = { args: { receipt: invalidReceipt }, play: async ({ canvas, userEvent }) => {
  await expect(canvas.getByText("Requests: Unknown field: request_total")).toBeVisible();
  await userEvent.click(canvas.getByRole("button", { name: /^Details/ }));
  await waitFor(() => expect(canvas.getByText(/Preview · Requests:/)).toHaveTextContent("Preview · Requests: Invalid · 0 ms"));
  await expect(canvas.getByText(/Preview · p95 latency:/)).toBeVisible();
} };
export const Corrected: Story = { args: { receipt: correctedReceipt }, play: async ({ canvas, userEvent }) => {
  await userEvent.click(canvas.getByRole("button", { name: /^Details/ }));
  await waitFor(() => expect(canvas.getByText("Fixed Requests: Unknown field: request_total")).toBeVisible());
  await expect(canvas.getByText("Fixed Requests")).toBeVisible();
  await expect(canvas.getByText(/Save check: checked/)).toBeVisible();
} };

export const SavedWithInvalidPanel: Story = { args: { receipt: savedInvalidReceipt }, play: async ({ canvas, userEvent }) => {
  await expect(canvas.getByText(/Saved v1/)).toBeVisible();
  await expect(canvas.getByText("Requests: Unknown field: request_total")).toBeVisible();
  await userEvent.click(canvas.getByRole("button", { name: /^Details/ }));
  await waitFor(() => expect(canvas.getByText(/Save · Requests:/)).toHaveTextContent("Save · Requests: Invalid · 0 rows · 0 ms"));
} };
