import { Group, Modal } from "@mantine/core";
import { ShortcutButton } from "./shortcut-button";
import { createContext, useEffect, useRef, useState, type ReactNode } from "react";

export const PanelTooltipContext = createContext<((opened: boolean) => void) | undefined>(undefined);

export function PanelFullscreen({ opened, onClose, title, children, returnFocusTo, shortcutScope, onShortcuts, escapeEnabled = true }: {
  shortcutScope?: string; onShortcuts?(): void; escapeEnabled?: boolean;
  opened: boolean; onClose(): void; title: string; children: ReactNode; returnFocusTo(): HTMLElement | undefined;
}) {
  const wasOpen = useRef(false);
  const [tooltipOpen, setTooltipOpen] = useState(false);
  const restore = useRef(returnFocusTo);
  restore.current = returnFocusTo;
  useEffect(() => {
    const closing = wasOpen.current && !opened;
    wasOpen.current = opened;
    if (!closing) return;
    // Covers URL navigation (including Back), not only the close callback.
    const frame = requestAnimationFrame(() => restore.current()?.focus());
    return () => cancelAnimationFrame(frame);
  }, [opened]);
  return <Modal.Root opened={opened} onClose={onClose} fullScreen trapFocus returnFocus={false} closeOnEscape={escapeEnabled && !tooltipOpen}>
    <Modal.Overlay />
    <Modal.Content aria-label={title} data-panel-fullscreen data-shortcut-scope={shortcutScope}>
      <Modal.Header><Group ml="auto" gap="xs">{onShortcuts && <ShortcutButton onClick={onShortcuts}/>}<Modal.CloseButton data-autofocus aria-label="Close panel view" /></Group></Modal.Header>
      <Modal.Body><PanelTooltipContext.Provider value={setTooltipOpen}>{children}</PanelTooltipContext.Provider></Modal.Body>
    </Modal.Content>
  </Modal.Root>;
}
