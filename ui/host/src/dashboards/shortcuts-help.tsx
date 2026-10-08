import { Group, Kbd, Modal, Stack, Text } from "@mantine/core";

export function ShortcutsHelp({opened, onClose, dashboard = true}: {opened: boolean; onClose(): void; dashboard?: boolean}) {
  const keys = [...(dashboard ? [["r", "Refresh now"], ["e", "Toggle layout mode"], ["h", "Open history"]] : []),
    ["f", "Open the focused panel in full-screen"], ["?", "Open keyboard shortcuts"], ["Escape", "Close the top dialog"]];
  return <Modal opened={opened} onClose={onClose} title="Keyboard shortcuts" trapFocus returnFocus closeOnEscape>
    <Stack gap="xs">
      {keys.map(([key, description]) => <Group key={key} gap="sm"><Kbd>{key}</Kbd><Text size="sm">{description}</Text></Group>)}
      <Text size="sm">Tab to a panel or any of its controls before pressing f. Without a focused panel, f does nothing.</Text>
      <Text size="sm">Choose Explore chart to focus a point. Left/Right moves between points; Up/Down changes series. Choose a series or point number to jump. Enter drills or applies the panel filter.</Text>
      <Text size="sm">On time charts, Shift+Left/Right extends a range. Review the UTC start and end, then choose Zoom to range. Use Reset zoom, Zoom out, or browser Back to return; comparison and filters are preserved.</Text>
      <Text size="sm">Shortcuts work while focus is in {dashboard ? "the dashboard" : "this fragment"}. They pause while typing, in menus, and while a dialog or drawer is open. Modifier shortcuts keep their browser behavior.</Text>
      <Text size="sm">The global / shortcut focuses the chat composer.</Text>
    </Stack>
  </Modal>;
}
