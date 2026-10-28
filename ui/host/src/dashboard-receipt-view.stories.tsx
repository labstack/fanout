import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor } from "storybook/test";
import { savedReceipt } from "./catalog/fixtures";
import { RouterFrame } from "./catalog/router-frame";
import { DashboardReceiptView } from "./dashboard-receipt-view";
const meta = {
  id: "components-dashboard-receipt", title: "Components/Dashboard receipt", component: DashboardReceiptView,
  args: { receipt: savedReceipt }, decorators: [Story => <RouterFrame><Story /></RouterFrame>],
} satisfies Meta<typeof DashboardReceiptView>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Saved: Story = {};
export const Details: Story = { play: async ({ canvas, userEvent }) => {
  await userEvent.click(canvas.getByRole("button", { name: "Details" }));
  await waitFor(() => expect(canvas.getByText(/Save check: checked/)).toBeVisible());
} };
export const Building: Story = { args: { receipt: { ...savedReceipt, saved: undefined, stages: { ...savedReceipt.stages, save: { state: "incomplete" } } }, running: true, awaitingAnswer: true, activity: "Building your dashboard…" } };
