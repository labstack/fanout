import { Box } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";
import { useEffect, useMemo, type ReactNode } from "react";
import Rail from "./rail";

function RailFrame({ children }: { children: ReactNode }) {
  const client = useMemo(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }), []);
  useEffect(() => () => client.clear(), [client]);
  return <QueryClientProvider client={client}><Box mih="100dvh" bg="var(--mantine-color-body)"><Box w={248} h="100dvh" p="sm">{children}</Box></Box></QueryClientProvider>;
}
const meta = {
  id: "layout-rail", title: "Layout/Rail", component: Rail,
  args: { agentAvailable: true, activeThreadID: "commerce-review", onNewChat: fn(), onSelectThread: fn(), onDeletedThread: fn(), onSelectDashboard: fn(), onCreateDashboard: fn(), onNewDashboard: fn(), onNavigate: fn() },
  parameters: { layout: "fullscreen" },
  decorators: [Story => <RailFrame><Story /></RailFrame>],
} satisfies Meta<typeof Rail>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Populated: Story = { play: async ({ canvas, userEvent, args }) => {
  const dashboard = await canvas.findByRole("link", {name: /Commerce health/});
  const name = dashboard.querySelector<HTMLElement>('[title="Commerce health"]')!;
  await document.fonts.ready;
  await expect(name).toHaveTextContent("Commerce health");
  await expect(name.scrollWidth).toBeLessThanOrEqual(name.clientWidth);
  await userEvent.click(await canvas.findByRole("button", { name: /^Commerce health/ }));
  await expect(args.onSelectThread).toHaveBeenCalledWith("commerce-review");
} };
export const Empty: Story = { parameters: { scenario: { empty: true } } };

export const Collapsed: Story = { args: { collapsed: true } };
export const SettingsActive: Story = { args: { activePath: "/settings" } };
