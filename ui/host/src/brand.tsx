export function BrandMark({ size = "regular" }: { size?: "small" | "regular" | "large" }) {
  return <span className={`brand-mark ${size}`} aria-hidden="true"><ResonanceMark /></span>;
}

export function ResonanceMark() {
  return (
    <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <circle cx="8" cy="12" r="2" fill="currentColor" stroke="none" />
      <path d="M12 8.5a5 5 0 0 1 0 7M15 5.5a9 9 0 0 1 0 13" />
      <path d="M5 6.5a8 8 0 0 0 0 11" />
    </svg>
  );
}
