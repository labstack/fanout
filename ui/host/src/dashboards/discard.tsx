import { DataState, Notice } from "@labstack/ui";
import { Box,  Button, Group, Text } from "@mantine/core";
import { useEffect, useRef, useState, type KeyboardEvent } from "react";

/**
 * Asks before a draft is thrown away. Escape asks, or closes when nothing
 * changed; Escape or Keep editing on the prompt puts focus back where it was,
 * so the next Escape still reaches the dialog.
 */
export function useDiscard(dirty: boolean, onClose: () => void, what: string) {
  const [confirming, setConfirming] = useState(false);
  const keep = useRef<HTMLButtonElement>(null);
  const before = useRef<HTMLElement | null>(null);
  const dialog = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (!confirming) return;
    dialog.current = keep.current?.closest<HTMLElement>("[role=dialog]") ?? null;
    keep.current?.focus();
  }, [confirming]);
  const request = () => {
    if (!dirty) { onClose(); return; }
    before.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setConfirming(true);
  };
  const resume = () => {
    setConfirming(false);
    const target = before.current?.isConnected && before.current !== document.body ? before.current : dialog.current;
    requestAnimationFrame(() => target?.focus());
  };
  // Escape first reaches an editor, which closes its completion list; only an unhandled Escape gets here.
  // A combobox closes its open list without marking the event handled, so its expanded state says so.
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key !== "Escape" || event.defaultPrevented || event.nativeEvent.isComposing) return;
    if (event.target instanceof Element && event.target.getAttribute("aria-expanded") === "true") return;
    if (confirming) resume(); else request();
  };
  const prompt = confirming && <Box mb="sm"><Notice tone="warning" title="Discard changes?">
    <Text size="sm">Your changes to {what} have not been saved.</Text>
    <Group gap="xs" mt="sm"><Button ref={keep} variant="default" onClick={resume}>Keep editing</Button><Button color="bad" onClick={onClose}>Discard changes</Button></Group>
  </Notice></Box>;
  return { request, onKeyDown, prompt };
}
