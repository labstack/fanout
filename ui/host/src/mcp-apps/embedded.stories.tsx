import { useComputedColorScheme } from "@mantine/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn } from "storybook/test";
import type { PanelFragment } from "../../../panels/fragment";
import { health, panels, results } from "../catalog/fixtures";
import { FragmentView } from "../dashboards/fragment-view";

import { QueryFrame } from "../catalog/query-frame";
import { healthRules } from "../catalog/accessibility";

const meta = {
  id: "embedded-analysis", title: "Embedded/Analysis views", component: FragmentView,
  args: { fragment: health, dark: false, onQuery: fn(async () => health), drillClient: { exemplars: fn(async () => ({ traces: [], truncated: false })), trace: fn(async () => { throw new Error("No trace fixture for this view"); }) } },
  render: function Render(args) { return <FragmentView {...args} dark={useComputedColorScheme("light") === "dark"} />; },
  decorators: [Story => <QueryFrame><Story /></QueryFrame>],
  parameters: { docs: { description: { component: "The shipping FragmentView, rendered directly for accessibility and text-layout checks. Page and flow stories additionally load the real built app in the shipping sandbox over mocked MCP." } } },
} satisfies Meta<typeof FragmentView>;
export default meta;
type Story = StoryObj<typeof meta>;
const fragment = (id: string): PanelFragment => ({ ...health, dashboard: { ...health.dashboard, panels: panels.filter(panel => panel.id === id) }, results: results.filter(result => result.id === id) });
export const Health: Story = { parameters: { a11y: { config: { rules: healthRules } } }, play: async ({ canvas }) => { await expect(await canvas.findByRole("region", { name: "Service health: degraded; 4 services" })).toBeVisible(); } };
export const Latency: Story = { args: { fragment: fragment("latency") } };
export const Dependencies: Story = { args: { fragment: fragment("topology") } };
