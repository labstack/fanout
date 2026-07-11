// jest-axe ships no type declarations of its own; declare the minimal
// surface this project uses. See ./vitest.d.ts for the matching
// `expect(...).toHaveNoViolations()` matcher augmentation.
declare module "jest-axe" {
  export interface AxeResults {
    violations: unknown[];
  }

  export function axe(
    container: Element | Document,
    options?: Record<string, unknown>,
  ): Promise<AxeResults>;

  export const toHaveNoViolations: {
    toHaveNoViolations(results: AxeResults): {
      pass: boolean;
      message: () => string;
    };
  };
}
