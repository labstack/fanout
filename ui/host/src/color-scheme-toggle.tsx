import { ActionIcon, Tooltip, useComputedColorScheme, useMantineColorScheme } from "@mantine/core";
import { Moon, Sun } from "@phosphor-icons/react";
import { useState } from "react";
export function ColorSchemeToggle() {
  const { setColorScheme } = useMantineColorScheme();
  // Reading the computed scheme rather than the stored one means the button
  // offers the opposite of what is on screen even while the setting is "auto".
  const scheme = useComputedColorScheme("light", { getInitialValueInEffect: true });
  const next = scheme === "dark" ? "light" : "dark";
  // The tooltip describes what the button will do, and the click changes that.
  // The label is reactive, so what stays on screen is not wrong — it is a
  // sentence rewriting itself under a stationary cursor, which reads as the
  // page arguing with itself. It closes on the click and comes back on the
  // next hover or focus.
  const [tip, setTip] = useState(false);
  return <Tooltip label={`Switch to ${next} theme`} opened={tip}>
    <ActionIcon variant="subtle" color="gray" aria-label={`Switch to ${next} theme`}
      onMouseEnter={() => setTip(true)} onMouseLeave={() => setTip(false)}
      onFocus={() => setTip(true)} onBlur={() => setTip(false)}
      onClick={() => { setTip(false); setColorScheme(next); }}>
      {scheme === "dark" ? <Sun size={17} weight="bold" /> : <Moon size={17} weight="bold" />}
    </ActionIcon>
  </Tooltip>;
}

