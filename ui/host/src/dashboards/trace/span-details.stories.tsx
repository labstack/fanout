import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, fn, within } from "storybook/test";
import { traceDetail } from "../../catalog/fixtures";
import { SpanDetails } from "./span-details";

const meta = {
  id: "components-span-details", title: "Components/Span details", component: SpanDetails,
  args: { traceId: traceDetail.data.trace_id, span: traceDetail.data.spans[0], onClose: fn(), load: async () => ({ attributes: { "http.response.status_code": 200, "http.request.method": "GET" }, resource: { "service.name": "checkout" }, events: [], scope: "fanout.http" }) },
  decorators: [Story => <QueryClientProvider client={new QueryClient()}><Story /></QueryClientProvider>],
} satisfies Meta<typeof SpanDetails>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Ready: Story = { play: async ({ canvas, userEvent, args }) => {
  // Assert the semantic description list and its actual machine values.
  const list = await canvas.findByLabelText("Attributes", { selector: "dl" });
  await expect(within(list).getByText("http.response.status_code")).toBeVisible();
  await expect(within(list).getByText("200")).toBeVisible();
  await expect(canvas.getByLabelText("Span metadata", { selector: "dl" })).toHaveTextContent(args.span.span_id);
  await expect(canvas.getByText("fanout.http")).toBeVisible();
  await userEvent.click(canvas.getByRole("button", { name: "Close span details" }));
  await expect(args.onClose).toHaveBeenCalledOnce();
} };
