import { Group, Kbd, Modal, Stack, Switch, Text } from "@mantine/core";
import { useShortcutPreference } from "./shortcut-preference";

export function ShortcutsHelp({opened, onClose, dashboard = true, fullscreen = false, enabled, onEnabled}: {opened: boolean; onClose(): void; dashboard?: boolean; fullscreen?: boolean; enabled?: boolean; onEnabled?(enabled: boolean): void}) {
  const fallback = useShortcutPreference("fragment");
  const keys = [...(dashboard || fullscreen ? [["r", "Refresh now"]] : []), ...(dashboard && !fullscreen ? [["e", "Toggle layout mode"], ["h", "Open history"]] : []),
    ["f", fullscreen ? "Exit full-screen" : "Open the focused panel in full-screen"], ["?", "Open keyboard shortcuts"], ["Escape", "Cancel a range, then leave the chart or close full-screen"]];
  return <Modal opened={opened} onClose={onClose} title="Keyboard shortcuts" trapFocus returnFocus closeOnEscape>
    <Stack gap="xs">
      <Switch label="Single-key shortcuts" checked={enabled ?? fallback.enabled} onChange={event => (onEnabled ?? fallback.change)(event.currentTarget.checked)}/>
      {keys.map(([key, description]) => <Group key={key} gap="sm"><Kbd>{key}</Kbd><Text size="sm">{description}</Text></Group>)}
      {!fullscreen && <Text size="sm">Focus a chart or a panel control before pressing f. Without a focused panel, f does nothing.</Text>}
      <Text size="sm">Tab directly to a chart. Left/Right moves between points; Up/Down changes series at the same time or category. Home/End jumps to the first/last point. Enter drills or applies the panel filter. Comparison and Other values can be read even when selection is unavailable.</Text>
      <Text size="sm">On time charts, Shift+Left/Right extends a range and pauses auto-refresh. Review the shaded range and compact UTC label, then choose Zoom to range. Use the time picker after zooming for precise edits. Escape or Cancel cancels it and resumes refresh. The next Escape leaves the chart or closes full-screen. Reset zoom, Zoom out and browser Back preserve comparison and filters.</Text>
      <Text size="sm">{fullscreen ? "In full-screen, r refreshes, f exits and ? opens help; e and h are inactive." : "Shortcuts work in the dashboard or the focused fragment."} Typing, menus and other dialogs or drawers pause shortcuts. Modifier shortcuts keep their browser behavior. Turning off single-key shortcuts keeps chart navigation and visible controls available.</Text>
      {dashboard && <Text size="sm">Outside chat fragments, the global / shortcut focuses the chat composer.</Text>}
    </Stack>
  </Modal>;
}
