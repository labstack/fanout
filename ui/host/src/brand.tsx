import { Group, Text, ThemeIcon, useComputedColorScheme } from "@mantine/core";
import { accent, fonts, surfaces } from "../../tokens";

type BrandSize = "small" | "regular" | "large";

const lockup = {
  small: { fontSize: 15, gap: 12 },
  regular: { fontSize: 18, gap: 14 },
  large: { fontSize: 22, gap: 16 },
} satisfies Record<BrandSize, { fontSize: number; gap: number }>;

export function BrandLockup({ size = "regular" }: { size?: BrandSize }) {
  const style = lockup[size];
  return (
    <Group component="span" gap={style.gap} wrap="nowrap" aria-label="Pitot">
      <BrandMark size={size} />
      <Text component="span" ff={fonts.body} fz={style.fontSize} fw={600} lh={1} lts="-0.045em">
        pitot
      </Text>
    </Group>
  );
}

export function BrandMark({ size = "regular" }: { size?: BrandSize }) {
  const pixels = { small: 32, regular: 46, large: 50 }[size];
  return <ThemeIcon size={pixels} variant="transparent" aria-hidden="true"><PitotMark /></ThemeIcon>;
}

// Full Waterfall geometry; the favicon uses a separate small-size cut.
function PitotMark() {
  const dark = useComputedColorScheme("light") === "dark";
  const ink = surfaces[dark ? "dark" : "light"].text;
  return (
    <svg viewBox="0 0 24 24" width="100%" height="100%" aria-hidden="true">
      <g fill="none" strokeWidth="2.6" strokeLinecap="round">
        <path stroke={accent[dark ? 5 : 7]} d="M3.3 4.5H20.7" />
        <path stroke={ink} d="M7 9.5H16.5" />
        <path stroke={ink} d="M7 14.5H11.5" />
        <path stroke={dark ? "#4A4A52" : "#B4B4BB"} d="M10.5 19.5H17" />
      </g>
    </svg>
  );
}
