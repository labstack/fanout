import { ThemeIcon } from "@mantine/core";

export function BrandMark({ size = "regular" }: { size?: "small" | "regular" | "large" }) {
  const pixels = { small: 32, regular: 46, large: 50 }[size];
  return <ThemeIcon size={pixels} radius={size === "small" ? "md" : "lg"} variant="gradient" gradient={{ from: "teal.7", to: "green.5", deg: 145 }} aria-hidden="true"><ResonanceMark /></ThemeIcon>;
}

export function ResonanceMark() {
  return (
    <svg viewBox="0 0 24 24" fill="none" width="58%" height="58%" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="8" cy="12" r="2" fill="currentColor" stroke="none" />
      <path d="M12 8.5a5 5 0 0 1 0 7M15 5.5a9 9 0 0 1 0 13" />
      <path d="M5 6.5a8 8 0 0 0 0 11" />
    </svg>
  );
}
