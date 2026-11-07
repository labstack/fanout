import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { TraceDetailView } from "../dashboards/trace/detail";
import { traceDetail } from "./fixtures";

const meta = {
  id: "components-trace-detail", title: "Components/Trace detail", component: TraceDetailView,
  args: { result: traceDetail, dark: false },
  render: (args, { globals }) => <TraceDetailView {...args} dark={globals.colorScheme === "dark"} />,
} satisfies Meta<typeof TraceDetailView>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Checkout: Story = { play: async ({ canvas }) => {
  await expect(canvas.getByText(traceDetail.data.trace_id, { exact: true })).toBeVisible();
  await expect(canvas.getByText(traceDetail.data.logs[0].body, { exact: true })).toBeVisible();
} };
