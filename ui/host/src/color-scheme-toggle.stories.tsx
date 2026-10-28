import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { ColorSchemeToggle } from "./color-scheme-toggle";
const meta = { id: "components-color-scheme-toggle", title: "Components/Color scheme toggle", component: ColorSchemeToggle, parameters: { interactiveColorScheme: true } } satisfies Meta<typeof ColorSchemeToggle>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Toggle: Story = { play: async ({ canvas, userEvent, globals }) => {
  const next = globals.colorScheme === "dark" ? "light" : "dark";
  await userEvent.click(await canvas.findByRole("button", { name: `Switch to ${next} theme` }));
  await expect(await canvas.findByRole("button", { name: `Switch to ${globals.colorScheme} theme` })).toBeVisible();
} };
