import { Box } from "@mantine/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { BrandLockup } from "./brand";
const meta = { id: "components-brand", title: "Components/Brand", component: BrandLockup, parameters: { layout: "fullscreen" }, decorators: [Story => <Box mih="100dvh" p="md" bg="var(--mantine-color-body)"><Story /></Box>] } satisfies Meta<typeof BrandLockup>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Small: Story = { args: { size: "small" } };
export const Regular: Story = { args: { size: "regular" } };
export const Large: Story = { args: { size: "large" } };
