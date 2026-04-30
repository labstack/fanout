/**
 * The single canonical variant vocabulary used across all variant-driven
 * primitives (Button, Badge, StatusBadge, …). Documented in PATTERNS.md.
 *
 * Banned domain words (`bullish`, `firing`, `error`, `destructive`, …) must
 * never appear here — domain-to-canonical mapping lives in
 * `lib/badge-variants.ts`.
 *
 * Each `cva` config in `components/ui/*.tsx` should structurally satisfy
 * `Partial<Record<CanonicalVariant, string>>` so adding a non-canonical key
 * (or typoing one) becomes a compile error.
 */
export type CanonicalVariant =
  | "default"
  | "secondary"
  | "success"
  | "danger"
  | "warning"
  | "info"
  | "neutral"
  | "outline"
  | "ghost"
  | "link";

export type CanonicalVariantMap = Partial<Record<CanonicalVariant, string>>;
