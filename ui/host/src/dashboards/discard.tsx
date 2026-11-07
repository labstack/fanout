import { ConfirmDialog } from "@labstack/ui";
import { Text } from "@mantine/core";
import { useRef, useState, type KeyboardEvent } from "react";

/**
 * Asks before a draft is thrown away. Escape asks, or closes when nothing
 * changed; Escape or Keep editing on the prompt puts focus back where it was,
 * so the next Escape still reaches the dialog.
 */
export function useDiscard(dirty: boolean, onClose: () => void, what: string) {
  const [confirming, setConfirming] = useState(false);
  const before = useRef<HTMLElement | null>(null);
  const request = () => {
    if (!dirty) { onClose(); return; }
    before.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setConfirming(true);
  };
  const resume = () => {
    setConfirming(false);
    const target = before.current?.isConnected && before.current !== document.body ? before.current : null;
    requestAnimationFrame(() => target?.focus());
  };
  // Escape first reaches an editor, which closes its completion list; only an unhandled Escape gets here.
  // A combobox closes its open list without marking the event handled, so its expanded state says so.
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key !== "Escape" || event.defaultPrevented || event.nativeEvent.isComposing) return;
    if (event.target instanceof Element && event.target.getAttribute("aria-expanded") === "true") return;
    if (confirming) resume(); else request();
  };
  const prompt = confirming && <ConfirmDialog opened={confirming} onClose={resume} title="Discard changes?" confirmLabel="Discard changes" cancelLabel="Keep editing" tone="danger" onConfirm={onClose}>
    Your changes to <Text span ff="monospace">{what}</Text> have not been saved.
  </ConfirmDialog>;
  return { request, onKeyDown, prompt };
}
