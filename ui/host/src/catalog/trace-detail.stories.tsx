import type { Meta, StoryObj } from "@storybook/react-vite";
import { contrastRatio } from "@labstack/ui";
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
export const Checkout: Story = { play: async ({ canvas, canvasElement, userEvent }) => {
  const toggle = canvas.getByRole("switch", { name: "Critical path" });
  await expect(toggle).toBeChecked();
  await expect(canvas.getAllByRole("img", { name: /on critical path/ })).toHaveLength(4);
  const segments = () => canvasElement.querySelectorAll<HTMLElement>("[data-critical-path-segment]");
  await expect(segments()).toHaveLength(6);
  const marked = segments()[0];
  const hex = (color: string) => "#" + color.match(/\d+/g)!.slice(0, 3).map(channel => Number(channel).toString(16).padStart(2, "0")).join("");
  await expect(contrastRatio(hex(getComputedStyle(marked).outlineColor), hex(getComputedStyle(marked.parentElement!).backgroundColor))).toBeGreaterThanOrEqual(3);
  await userEvent.click(toggle);
  await expect(toggle).not.toBeChecked();
  await expect(segments()).toHaveLength(0);
  await userEvent.click(toggle);
  await expect(segments()).toHaveLength(6);
  await expect(canvas.getByText(traceDetail.data.trace_id, { exact: true })).toBeVisible();
  await expect(canvas.getByText(traceDetail.data.logs[0].body, { exact: true })).toBeVisible();
} };
