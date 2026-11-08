import { Button, Group, Select, Stack, Text, Textarea, TextInput } from "@mantine/core";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";

function Controls() {
  return <Stack gap="xl">{(["sm", "md"] as const).map(size => <Stack key={size} gap="xs" role="group" aria-label={`${size} controls`}>
    <Text fw={600}>{size}</Text>
    <Group align="flex-end" gap="sm" wrap="wrap">
      <TextInput size={size} label={`${size} input`} placeholder="Service name" w={180} />
      <Select size={size} label={`${size} select`} placeholder="Choose a service" data={["checkout", "payments"]} w={180} />
      <Textarea size={size} label={`${size} textarea`} placeholder="Description" autosize minRows={1} w={180} />
      <Button size={size} variant="default">Default {size}</Button>
      <Button size={size}>Primary {size}</Button>
    </Group>
  </Stack>)}</Stack>;
}
const meta = { id: "components-controls", title: "Components/Controls", component: Controls } satisfies Meta<typeof Controls>;
export default meta;
type Story = StoryObj<typeof meta>;
export const Defaults: Story = { play: async ({ canvas, userEvent }) => {
  for (const [size, height] of [["sm", 32], ["md", 36]] as const) {
    const row = within(canvas.getByRole("group", { name: `${size} controls` }));
    const input = row.getByRole("textbox", { name: `${size} input` });
    const button = row.getByRole("button", { name: `Default ${size}` });
    const fieldStyle = getComputedStyle(input), buttonStyle = getComputedStyle(button);
    // Colors settle after the scheme switch, so wait out border and fill transitions.
    for (const property of ["borderTopColor", "backgroundColor", "borderTopLeftRadius", "height"] as const) await waitFor(() => expect(fieldStyle[property], `${size} ${property}`).toBe(buttonStyle[property]));
    await expect(input.getBoundingClientRect().height).toBe(height);
    await expect(fieldStyle.borderTopWidth).toBe("1px");
    await userEvent.type(input, "checkout");
    await expect(input).toHaveValue("checkout");
    await userEvent.click(row.getByLabelText(`${size} select`));
    await userEvent.click(await within(document.body).findByRole("option", { name: "payments" }));
    await expect(row.getByLabelText(`${size} select`)).toHaveValue("payments");
  }
} };
