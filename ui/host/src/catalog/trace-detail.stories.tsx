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
  const toggle = canvas.getByRole("switch", { name: /^Critical path/ });
  await expect(toggle).toBeChecked();
  await expect(canvas.getAllByRole("img", { name: /on critical path/ })).toHaveLength(4);
  const segments = () => canvasElement.querySelectorAll<HTMLElement>("[data-critical-path-segment]");
  await expect(segments()).toHaveLength(6);
  const hex = (color: string) => "#" + color.match(/[\d.]+/g)!.slice(0, 3).map(channel => Math.round(Number(channel) * (color.startsWith("color(srgb") ? 255 : 1)).toString(16).padStart(2, "0")).join("");
  for (const marked of segments()) {
    // The band is outlined by a body-colored halo, so it contrasts with its halo on any fill.
    const style = getComputedStyle(marked);
    const halo = style.boxShadow.match(/(?:rgba?|color)\([^)]*\)/)![0];
    await expect(contrastRatio(hex(style.backgroundColor), hex(halo))).toBeGreaterThanOrEqual(3);
    await expect(getComputedStyle(marked).height).toBe("3px");
  }
  await userEvent.click(toggle);
  await expect(toggle).not.toBeChecked();
  await expect(segments()).toHaveLength(0);
  await expect(canvas.queryAllByRole("img", { name: /on critical path/ })).toHaveLength(0);
  await userEvent.click(toggle);
  await expect(segments()).toHaveLength(6);
  await userEvent.click(canvas.getByRole("tab", { name: "Flame graph" }));
  await expect(canvas.getByText("Critical path", { exact: true })).toBeVisible();
  await expect(toggle).toBeDisabled();
  await userEvent.click(canvas.getByRole("tab", { name: "Waterfall" }));
  await expect(toggle).toBeEnabled();
  await expect(segments()).toHaveLength(6);
  await expect(canvas.getByText(traceDetail.data.trace_id, { exact: true })).toBeVisible();
  await expect(canvas.getByText(traceDetail.data.logs[0].body, { exact: true })).toBeVisible();
} };

export const CheckoutWithError: Story = {
  ...Checkout,
  args: { result: { ...traceDetail, data: { ...traceDetail.data, has_error: true, spans: traceDetail.data.spans.map((span, i) => i === 0 ? { ...span, status: "STATUS_CODE_ERROR" } : span) } } },
};
