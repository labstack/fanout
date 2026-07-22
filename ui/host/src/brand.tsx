import { useId } from "react";
import { Group, Text, ThemeIcon } from "@mantine/core";

type BrandSize = "small" | "regular" | "large";

const lockup = {
  small: { fontSize: 15, gap: 12, tracking: "0.16em" },
  regular: { fontSize: 18, gap: 14, tracking: "0.17em" },
  large: { fontSize: 22, gap: 16, tracking: "0.18em" },
} satisfies Record<BrandSize, { fontSize: number; gap: number; tracking: string }>;

export function BrandLockup({ size = "regular" }: { size?: BrandSize }) {
  const style = lockup[size];
  return (
    <Group component="span" gap={style.gap} wrap="nowrap" aria-label="Fanout">
      <BrandMark size={size} />
      <Text component="span" fz={style.fontSize} fw={800} lh={1} lts={style.tracking} tt="uppercase">
        Fanout
      </Text>
    </Group>
  );
}

export function BrandMark({ size = "regular" }: { size?: BrandSize }) {
  const pixels = { small: 32, regular: 46, large: 50 }[size];
  return <ThemeIcon size={pixels} variant="transparent" aria-hidden="true"><FanoutMark /></ThemeIcon>;
}

// "Flowing F" mark: three rounded ribbons forming an abstract F (no tile).
// Keep the ribbon paths and gradients in sync with ui/host/public/favicon.svg,
// site/public/favicon.svg, and the inline mark in
// site/src/components/site/Nav.astro; the favicons additionally carry a
// drop-shadow filter that this component omits.
export function FanoutMark() {
  const uid = useId().replace(/[^a-zA-Z0-9-]/g, "");
  const top = `fo-top-${uid}`;
  const mid = `fo-mid-${uid}`;
  const bot = `fo-bot-${uid}`;
  return (
    <svg viewBox="35 44 200 200" width="100%" height="100%" aria-hidden="true">
      <defs>
        <linearGradient id={top} x1="54" y1="52" x2="210" y2="104" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#5FE8CE" />
          <stop offset="0.55" stopColor="#81E4B9" />
          <stop offset="1" stopColor="#D9F276" />
        </linearGradient>
        <linearGradient id={mid} x1="58" y1="112" x2="176" y2="154" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#536FFF" />
          <stop offset="0.52" stopColor="#41B6F8" />
          <stop offset="1" stopColor="#66D0EE" />
        </linearGradient>
        <linearGradient id={bot} x1="58" y1="166" x2="145" y2="220" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#725BFF" />
          <stop offset="0.52" stopColor="#9A50F4" />
          <stop offset="1" stopColor="#CB55E8" />
        </linearGradient>
      </defs>
      <path d="M58 116V88C58 67 75 52 96 52H191C204 52 212 61 212 72C212 84 203 94 191 94H101C82 94 67 102 58 116Z" fill={`url(#${top})`} />
      <path d="M58 170V139C58 120 72 107 91 107H162C174 107 182 115 182 126C182 137 174 145 162 145H99C79 145 66 154 58 170Z" fill={`url(#${mid})`} />
      <path d="M58 219V188C58 170 71 157 89 157H126C138 157 146 165 146 176C146 187 138 195 126 195H100C89 195 84 200 84 211C84 225 74 235 61 235H58Z" fill={`url(#${bot})`} />
    </svg>
  );
}
