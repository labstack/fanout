import { railRules } from "./catalog/accessibility";
import { Box } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";
import { useEffect, useMemo, type ReactNode } from "react";
import Rail from "./rail";

function RailFrame({ children }: { children: ReactNode }) {
  const client = useMemo(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }), []);
  useEffect(() => () => client.clear(), [client]);
  return <QueryClientProvider client={client}><Box w={256} h={700} p="sm">{children}</Box></QueryClientProvider>;
}
const meta = {
  id: "layout-rail", title: "Layout/Rail", component: Rail,
  args: { agentAvailable: true, activeThreadID: "commerce-review", onNewChat: fn(), onSelectThread: fn(), onDeletedThread: fn(), onSelectDashboard: fn(), onCreateDashboard: fn() },
  parameters: { a11y: { config: { rules: railRules } } },
  decorators: [Story => <RailFrame><Story /></RailFrame>],
} satisfies Meta<typeof Rail>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Populated: Story = { play: async ({ canvas, userEvent, args }) => {
  await userEvent.click(await canvas.findByRole("button", { name: /^Commerce health/ }));
  await expect(args.onSelectThread).toHaveBeenCalledWith("commerce-review");
} };
export const Empty: Story = { parameters: { scenario: { empty: true } } };
