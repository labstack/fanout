import type { AxeResults } from "jest-axe";

// `src/test/setup.ts` calls `expect.extend(toHaveNoViolations)`; this
// augmentation gives that matcher a type so `bun run typecheck` sees it.
declare module "vitest" {
  interface Assertion<T = unknown> {
    toHaveNoViolations: T extends AxeResults ? () => void : never;
  }
}
