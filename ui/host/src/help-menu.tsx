import { ActionIcon, Kbd, Menu, Tooltip } from "@mantine/core";
import { BookOpen, Keyboard, Question } from "@phosphor-icons/react";
import { useRef } from "react";
import { docs } from "../../links";
import { shortcutsEvent, useShortcutsAvailable } from "./shortcuts-event";

/** Header help: documentation, and the current page's keyboard shortcuts when it has any. */
export function HelpMenu() {
  const shortcuts = useShortcutsAvailable();
  const trigger = useRef<HTMLButtonElement>(null);
  /* The dialog returns focus to whatever held it when it opened. The menu
     item disappears as the menu closes, so focus moves to Help first and
     Escape lands back there. */
  const openShortcuts = () => { trigger.current?.focus(); window.dispatchEvent(new Event(shortcutsEvent)); };
  return <Menu position="bottom-end" withinPortal shadow="md">
    <Menu.Target>
      <Tooltip label="Help"><ActionIcon ref={trigger} variant="subtle" color="gray" aria-label="Help"><Question size={18} weight="bold" /></ActionIcon></Tooltip>
    </Menu.Target>
    <Menu.Dropdown>
      <Menu.Item component="a" href={docs.home} target="_blank" rel="noopener noreferrer" leftSection={<BookOpen size={15} />}>Documentation</Menu.Item>
      {shortcuts && <Menu.Item leftSection={<Keyboard size={15} />} rightSection={<Kbd size="xs">?</Kbd>} onClick={openShortcuts}>Keyboard shortcuts</Menu.Item>}
    </Menu.Dropdown>
  </Menu>;
}
