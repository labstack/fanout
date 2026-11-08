import { ConfirmDialog } from "@labstack/ui";
import { Text } from "@mantine/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, within } from "storybook/test";

const meta = {
  id: "components-confirm-dialog", title: "Components/Confirm dialog", component: ConfirmDialog,
  args: { opened: true, title: "Delete chat?", confirmLabel: "Delete chat", tone: "danger", onClose: fn(), onConfirm: fn(), children: <>Permanently delete <Text span ff="monospace">Checkout latency</Text> and its saved messages.</> },
} satisfies Meta<typeof ConfirmDialog>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Danger: Story = { play: async ({ userEvent, args }) => {
  const dialog = within(await within(document.body).findByRole("dialog", { name: "Delete chat?" }));
  await expect(dialog.getByText("Checkout latency")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toHaveFocus();
  await userEvent.click(dialog.getByRole("button", { name: "Delete chat" }));
  await expect(args.onConfirm).toHaveBeenCalledOnce();
} };
export const Pending: Story = { args: { pending: "confirm" }, play: async ({ userEvent, args }) => {
  const dialog = within(await within(document.body).findByRole("dialog"));
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeDisabled();
  await userEvent.keyboard("{Escape}");
  await expect(args.onClose).not.toHaveBeenCalled();
  await expect(within(document.body).getByRole("dialog")).toBeVisible();
} };
export const Error: Story = { args: { error: "The chat could not be deleted. Try again." }, play: async ({ userEvent, args }) => {
  const dialog = within(await within(document.body).findByRole("dialog"));
  await expect(dialog.getByRole("alert")).toHaveTextContent("The chat could not be deleted. Try again.");
  await userEvent.click(dialog.getByRole("button", { name: "Delete chat" }));
  await expect(args.onConfirm).toHaveBeenCalledOnce();
} };
