import { ActionIcon, Tooltip } from "@mantine/core";
import { Keyboard } from "@phosphor-icons/react";
export function ShortcutButton({onClick}: {onClick(): void}) {
  return <Tooltip label="Keyboard shortcuts (?)"><ActionIcon variant="default" size="lg" aria-label="Keyboard shortcuts (?)" onClick={onClick}><Keyboard size={16}/></ActionIcon></Tooltip>;
}
