import { DataState } from "@labstack/ui";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { assertLoadingState } from "./catalog/loading";
import { LoadingSkeleton } from "./loading-skeleton";

const meta = {
  id: "components-loading-skeleton", title: "Components/Loading skeleton", component: LoadingSkeleton,
  args: { shape: "key_value", rows: 3 },
  render: args => <DataState state="loading" loadingLabel="Loading details" skeleton={<LoadingSkeleton {...args} />} />,
} satisfies Meta<typeof LoadingSkeleton>;
export default meta;
type Story = StoryObj<typeof meta>;

export const KeyValue: Story = { play: async ({ canvas }) => {
  const placeholder = await assertLoadingState(await canvas.findByText("Loading details"), "Loading details", "key_value");
  await expect(placeholder.querySelector('[data-skeleton-row="header"]')).toBeNull();
  const labels = placeholder.querySelectorAll<HTMLElement>("dt");
  const values = placeholder.querySelectorAll<HTMLElement>("dd");
  await expect(labels).toHaveLength(3);
  await expect(values).toHaveLength(3);
  for (const [index, label] of labels.entries()) {
    await expect(label.getBoundingClientRect().width).toBe(192);
    await expect(values[index].getBoundingClientRect().left).toBe(label.getBoundingClientRect().right);
    await expect(placeholder.querySelector(`[data-skeleton-value="${index}"]`)?.getBoundingClientRect().height).toBe(21);
  }
} };
export const Settings: Story = { args: { shape: "settings" }, play: async ({ canvas }) => {
  const placeholder = await assertLoadingState(await canvas.findByText("Loading details"), "Loading details", "settings");
  await expect(placeholder.querySelectorAll('[data-skeleton-section]')).toHaveLength(3);
} };
export const ConnectedAgents: Story = { args: { shape: "agents" }, play: async ({ canvas }) => {
  const placeholder = await assertLoadingState(await canvas.findByText("Loading details"), "Loading details", "agents");
  const buttons = [...placeholder.querySelectorAll('.mantine-Skeleton-root')].filter(element => element.getBoundingClientRect().width === 96);
  await expect(buttons).toHaveLength(3);
  for (const button of buttons) await expect(button.getBoundingClientRect().height).toBe(36);
} };
