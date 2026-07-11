# Frontend Rewrite — Plan 1: Foundation & Harness

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up `web-next/` — a fresh React SPA with the "quiet instrument" design system, a typed/validated data layer, an app shell, and a minimal `/api/overview`-backed Home summary line — served by the Go binary behind a feature flag so the real embed/serve path is exercised from day one.

**Architecture:** Clean-room rewrite in a parallel `web-next/` dir (swaps to `web/` at parity, a later plan). Feature-first structure; Tailwind v4 `@theme` tokens; TanStack Query + a zod-validated typed `fetch` client with a refresh interceptor; URL-owned view state (namespace) via a query-key factory. The Go side gains a second embed package (`internal/uinext`) selected by env, so the old UI stays default while `web-next` is continuously build/serve-tested.

**Tech Stack:** React 19, Vite 8, TypeScript (strict + `noUncheckedIndexedAccess`), Tailwind v4, TanStack Query 5, Zustand 5, Radix + CVA + tailwind-merge, zod 4, Phosphor icons, Vitest + Testing Library + jest-axe. Go 1.x (Echo v5) for the embed/serve wiring.

## Global Constraints

Copied verbatim from `docs/superpowers/specs/2026-07-10-frontend-clean-rewrite-design.md`. Every task implicitly includes these.

- **No porting** of the monk-era `web/src/*.tsx`. The generator (`cmd/genblocks`), Go structs, and build infra are fair reference; the old TSX is not.
- **TypeScript strict + `noUncheckedIndexedAccess`.** No `any`; no non-null `!` outside guarded scopes. `verbatimModuleSyntax`, `import type`.
- **File naming: kebab-case.** One component per file. No barrel-file re-export sprawl.
- **Styling: tokens only.** No inline hex in `className` (ESLint-enforced). uPlot colors go through a CSS-var→canvas bridge (the one exception; not used in this plan).
- **Color is severity + shape + label**, never hue alone. `--ink-3` is a non-text token (hairlines only); all labels use `--ink-2`. Colored numbers use the `-text` status ramp.
- **Data layer:** one typed `fetch` wrapper → zod `safeParse` (log + degrade, never throw) → TanStack Query. No `fetch`/`useQuery` inline in view components. Token is **never** in a query key. Namespace threads through a query-key factory.
- **A11y + reduced-motion are acceptance criteria.** `eslint-plugin-jsx-a11y` + a jest-axe smoke per surface.
- **Both themes first-class**; explicit `data-theme` overrides `prefers-color-scheme` in both directions.
- **Commit after every green step.**

## File Structure (this plan)

```
web-next/
  package.json            # deps + scripts (dev/build/lint/typecheck/test)
  index.html
  vite.config.ts          # react + tailwind plugins, port 7523, /api+/mcp proxy → :7520
  tsconfig.json           # references
  tsconfig.app.json       # strict + noUncheckedIndexedAccess, @/* alias
  tsconfig.node.json
  eslint.config.js        # tseslint + react-hooks + jsx-a11y + hex ban
  vitest.config.ts
  src/
    main.tsx              # entry: providers + router mount
    vite-env.d.ts
    test/setup.ts         # jest-dom + jest-axe matchers
    styles/
      tokens.css          # @theme + :root light/dark, status ramps
      globals.css         # base element styles, font-face
      fonts/              # (geist woff2 vendored later; system fallback now)
    shared/
      lib/
        cn.ts             # class-merge helper
        api-client.ts     # typed fetch + refresh interceptor + ApiError
        query-keys.ts     # namespace-aware key factory
        query-client.ts   # QueryClient instance + defaults
        schemas/
          overview.ts     # zod schema for /api/overview (first hand-written; codegen replaces later)
      ui/
        button.tsx        # CVA primitive (needed by shell)
      hooks/
        use-namespace.ts  # reads namespace from URL search params
    app/
      providers.tsx       # QueryClientProvider + ThemeProvider
      router.tsx          # routes: / (home), placeholders for others
      root-layout.tsx     # rail + <Outlet/>
      rail.tsx            # four-surface nav (Phosphor icons)
      theme-provider.tsx  # data-theme management
    features/
      home/
        home-page.tsx     # minimal: summary line from useOverview
        use-overview.ts   # TanStack Query hook
    components/
      states/
        loading-state.tsx
        error-state.tsx
        empty-state.tsx

internal/uinext/
  uinext.go               # //go:embed all:dist ; Dist() mirror of internal/ui
  dist/.gitkeep           # placeholder so embed compiles unbuilt

cmd/fanout/main.go        # MODIFY: env-gated selection of ui vs uinext
justfile                  # MODIFY: build-next target
```

---

### Task 1: Scaffold `web-next/` (build tooling that runs)

**Files:**
- Create: `web-next/package.json`, `web-next/index.html`, `web-next/vite.config.ts`, `web-next/tsconfig.json`, `web-next/tsconfig.app.json`, `web-next/tsconfig.node.json`, `web-next/eslint.config.js`, `web-next/vitest.config.ts`, `web-next/src/main.tsx`, `web-next/src/vite-env.d.ts`, `web-next/src/test/setup.ts`, `web-next/.gitignore`

**Interfaces:**
- Produces: runnable scripts `dev`, `build`, `lint`, `typecheck`, `test`; `@/*` → `src/*` alias; a mounting point in `main.tsx` that later tasks replace.

- [ ] **Step 1: Create `web-next/package.json`**

```json
{
  "name": "web-next",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "lint": "eslint .",
    "typecheck": "tsc -b --noEmit",
    "test": "vitest run",
    "test:watch": "vitest"
  },
  "dependencies": {
    "@phosphor-icons/react": "^2.1.7",
    "@radix-ui/react-slot": "^1.2.2",
    "@tanstack/react-query": "^5.99.0",
    "class-variance-authority": "^0.7.1",
    "clsx": "^2.1.1",
    "react": "^19.2.5",
    "react-dom": "^19.2.5",
    "react-hook-form": "^7.73.1",
    "@hookform/resolvers": "^5.2.2",
    "react-router": "^7.14.0",
    "tailwind-merge": "^3.5.0",
    "zod": "^4.3.6",
    "zustand": "^5.0.12"
  },
  "devDependencies": {
    "@eslint/js": "^10.0.1",
    "@tailwindcss/vite": "^4.2.2",
    "@testing-library/dom": "^10.4.0",
    "@testing-library/jest-dom": "^6.6.3",
    "@testing-library/react": "^16.3.0",
    "@testing-library/user-event": "^14.5.2",
    "@types/node": "^25.6.0",
    "@types/react": "^19.2.14",
    "@types/react-dom": "^19.2.3",
    "@vitejs/plugin-react": "^6.0.1",
    "eslint": "^10.2.0",
    "eslint-plugin-jsx-a11y": "^6.10.2",
    "eslint-plugin-react-hooks": "^7.0.1",
    "eslint-plugin-react-refresh": "^0.5.2",
    "globals": "^17.5.0",
    "jest-axe": "^9.0.0",
    "jsdom": "^25.0.1",
    "tailwindcss": "^4.2.2",
    "typescript": "~6.0.2",
    "typescript-eslint": "^8.58.1",
    "vite": "^8.0.8",
    "vitest": "^3.0.0"
  }
}
```

- [ ] **Step 2: Create `web-next/index.html`**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Fanout</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 3: Create `web-next/vite.config.ts`** (dev proxy to the running Go server on :7520)

```ts
import path from "path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 7523,
    host: true,
    proxy: {
      "/api": { target: "http://localhost:7520", changeOrigin: true },
      "/mcp": { target: "http://localhost:7520", changeOrigin: true },
    },
  },
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
});
```

- [ ] **Step 4: Create the three tsconfig files**

`web-next/tsconfig.json`:
```json
{
  "compilerOptions": { "paths": { "@/*": ["./src/*"] } },
  "files": [],
  "references": [
    { "path": "./tsconfig.app.json" },
    { "path": "./tsconfig.node.json" }
  ]
}
```

`web-next/tsconfig.app.json`:
```json
{
  "compilerOptions": {
    "tsBuildInfoFile": "./node_modules/.tmp/tsconfig.app.tsbuildinfo",
    "target": "ES2022",
    "useDefineForClassFields": true,
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "types": ["vite/client"],
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true,
    "moduleDetection": "force",
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "erasableSyntaxOnly": true,
    "noFallthroughCasesInSwitch": true,
    "noUncheckedSideEffectImports": true,
    "paths": { "@/*": ["./src/*"] }
  },
  "include": ["src"]
}
```

`web-next/tsconfig.node.json`:
```json
{
  "compilerOptions": {
    "tsBuildInfoFile": "./node_modules/.tmp/tsconfig.node.tsbuildinfo",
    "target": "ES2023",
    "lib": ["ES2023"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "verbatimModuleSyntax": true,
    "moduleDetection": "force",
    "noEmit": true,
    "strict": true,
    "noUncheckedIndexedAccess": true
  },
  "include": ["vite.config.ts", "vitest.config.ts"]
}
```

- [ ] **Step 5: Create `web-next/eslint.config.js`** (carry forward the hex ban; add jsx-a11y)

```js
import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import jsxA11y from "eslint-plugin-jsx-a11y";
import tseslint from "typescript-eslint";

const BANNED_CLASSNAMES = [
  {
    selector:
      "JSXAttribute[name.name='className'] Literal[value=/(?:^|\\s)(?:text|bg|border|ring|fill|stroke|from|to|via)-\\[#[0-9a-fA-F]/]",
    message:
      "Use semantic tokens (text-ink, bg-crit/10, border-line, …) instead of hex literals.",
  },
];

export default tseslint.config(
  { ignores: ["dist", "vite.config.ts", "vitest.config.ts", "eslint.config.js"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: { ecmaVersion: 2022, globals: globals.browser },
    plugins: {
      "react-hooks": reactHooks,
      "react-refresh": reactRefresh,
      "jsx-a11y": jsxA11y,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      ...jsxA11y.flatConfigs.recommended.rules,
      "@typescript-eslint/no-explicit-any": "error",
      "@typescript-eslint/no-non-null-assertion": "error",
      "@typescript-eslint/consistent-type-imports": "error",
      "no-restricted-syntax": ["error", ...BANNED_CLASSNAMES],
    },
  },
);
```

- [ ] **Step 6: Create `web-next/vitest.config.ts` and `src/test/setup.ts`**

`web-next/vitest.config.ts`:
```ts
import path from "path";
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
});
```

`web-next/src/test/setup.ts`:
```ts
import "@testing-library/jest-dom/vitest";
import { expect } from "vitest";
import { toHaveNoViolations } from "jest-axe";

expect.extend(toHaveNoViolations);
```

- [ ] **Step 7: Create `src/vite-env.d.ts`, `src/main.tsx`, `.gitignore`**

`web-next/src/vite-env.d.ts`:
```ts
/// <reference types="vite/client" />
```

`web-next/src/main.tsx` (placeholder mount; replaced in Task 6):
```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

const root = document.getElementById("root");
if (!root) throw new Error("root element missing");
createRoot(root).render(
  <StrictMode>
    <div>fanout web-next</div>
  </StrictMode>,
);
```

`web-next/.gitignore`:
```
node_modules
dist
*.tsbuildinfo
```

- [ ] **Step 8: Install and verify the toolchain runs**

Run: `cd web-next && bun install && bun run build && bun run lint && bun test`
Expected: `bun install` resolves; `build` emits `dist/index.html`; `lint` passes with no errors; `test` reports "No test files found" (exit 0) — that's fine at this stage.

- [ ] **Step 9: Commit**

```bash
git add web-next/ && git commit -m "feat(web-next): scaffold vite+react+ts strict toolchain"
```

---

### Task 2: Design tokens + globals (the "quiet instrument" palette)

**Files:**
- Create: `web-next/src/styles/tokens.css`, `web-next/src/styles/globals.css`
- Test: `web-next/src/styles/contrast.test.ts`

**Interfaces:**
- Produces: CSS custom properties + Tailwind `@theme` slots: `--paper --raised --raised-2 --line --ink --ink-2 --ink-3`, `--accent --accent-fg`, status `--ok --warn --crit --info` each with `-bg` and `-text`, `--gen-bg`, `--radius`, motion durations. Utilities `bg-paper text-ink text-ink-2 border-line text-ok-text` etc.

- [ ] **Step 1: Write the failing contrast test** (guards the a11y fix from review)

`web-next/src/styles/contrast.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Minimal WCAG relative-luminance contrast, hex → ratio.
function ratio(hex1: string, hex2: string): number {
  const lum = (hex: string) => {
    const n = hex.replace("#", "");
    const ch = [0, 2, 4].map((i) => parseInt(n.slice(i, i + 2), 16) / 255);
    const lin = ch.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
    const [r, g, b] = lin as [number, number, number];
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const a = lum(hex1), b = lum(hex2);
  const [hi, lo] = a > b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

// Pull a token value out of tokens.css by name within the :root{...} light block.
function token(css: string, name: string): string {
  const m = css.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`));
  if (!m) throw new Error(`token ${name} not found`);
  return m[1]!;
}

const css = readFileSync(
  fileURLToPath(new URL("./tokens.css", import.meta.url)),
  "utf8",
);

describe("light-mode token contrast (WCAG)", () => {
  it("body/muted text clears 4.5:1 on paper", () => {
    expect(ratio(token(css, "--ink"), token(css, "--paper"))).toBeGreaterThanOrEqual(4.5);
    expect(ratio(token(css, "--ink-2"), token(css, "--paper"))).toBeGreaterThanOrEqual(4.5);
  });
  it("status TEXT ramp clears 4.5:1 on paper", () => {
    for (const t of ["--ok-text", "--warn-text", "--crit-text", "--info-text"]) {
      expect(ratio(token(css, t), token(css, "--paper"))).toBeGreaterThanOrEqual(4.5);
    }
  });
});
```

- [ ] **Step 2: Run it — must fail** (file not created yet)

Run: `cd web-next && bun test contrast`
Expected: FAIL — cannot read `tokens.css` / tokens not found.

- [ ] **Step 3: Create `web-next/src/styles/tokens.css`** (values tuned so the test passes)

```css
@import "tailwindcss";

@custom-variant dark (&:where([data-theme="dark"], [data-theme="dark"] *));

@theme inline {
  --color-paper: var(--paper);
  --color-raised: var(--raised);
  --color-raised-2: var(--raised-2);
  --color-line: var(--line);
  --color-ink: var(--ink);
  --color-ink-2: var(--ink-2);
  --color-ink-3: var(--ink-3);
  --color-accent: var(--accent);
  --color-accent-fg: var(--accent-fg);
  --color-ok: var(--ok);
  --color-ok-bg: var(--ok-bg);
  --color-ok-text: var(--ok-text);
  --color-warn: var(--warn);
  --color-warn-bg: var(--warn-bg);
  --color-warn-text: var(--warn-text);
  --color-crit: var(--crit);
  --color-crit-bg: var(--crit-bg);
  --color-crit-text: var(--crit-text);
  --color-info: var(--info);
  --color-info-bg: var(--info-bg);
  --color-info-text: var(--info-text);
  --color-gen-bg: var(--gen-bg);
  --radius: 0.625rem;
  --duration-100: 100ms;
  --duration-200: 200ms;
  --duration-300: 300ms;
  --ease-default: cubic-bezier(0.4, 0, 0.2, 1);
}

/* Light (default). ink-3 is hairline-only; labels use ink-2. */
:root {
  color-scheme: light;
  --paper: #f7f8f9;
  --raised: #ffffff;
  --raised-2: #f0f2f4;
  --line: #e4e7ea;
  --ink: #14171d;
  --ink-2: #565b66;
  --ink-3: #878d99;
  --accent: #14171d;
  --accent-fg: #ffffff;
  --ok: #3f9d78;   --ok-bg: #eaf4ef;   --ok-text: #2b6f52;
  --warn: #b7852b; --warn-bg: #f7efdf; --warn-text: #855f0f;
  --crit: #cf574d; --crit-bg: #fbebe9; --crit-text: #b23127;
  --info: #4f7fb0; --info-bg: #eaf1f8; --info-text: #396792;
  --gen-bg: #f4f2fb;
}

@media (prefers-color-scheme: dark) {
  :root {
    color-scheme: dark;
    --paper: #0b0e14;
    --raised: #12151d;
    --raised-2: #1a1f2c;
    --line: #20242e;
    --ink: #c5c8ce;
    --ink-2: #7d8390;
    --ink-3: #5c626e;
    --accent: #e8eaed;
    --accent-fg: #0b0e14;
    --ok: #4bb489;   --ok-bg: #0f1a17;   --ok-text: #4bb489;
    --warn: #cf9d42; --warn-bg: #1c1810; --warn-text: #cf9d42;
    --crit: #e0685d; --crit-bg: #1e1210; --crit-text: #e0685d;
    --info: #6b9bd0; --info-bg: #0f1620; --info-text: #6b9bd0;
    --gen-bg: #14121c;
  }
}

/* Explicit toggle wins in both directions */
:root[data-theme="light"] { color-scheme: light;
  --paper:#f7f8f9;--raised:#ffffff;--raised-2:#f0f2f4;--line:#e4e7ea;
  --ink:#14171d;--ink-2:#565b66;--ink-3:#878d99;--accent:#14171d;--accent-fg:#ffffff;
  --ok:#3f9d78;--ok-bg:#eaf4ef;--ok-text:#2b6f52;--warn:#b7852b;--warn-bg:#f7efdf;--warn-text:#855f0f;
  --crit:#cf574d;--crit-bg:#fbebe9;--crit-text:#b23127;--info:#4f7fb0;--info-bg:#eaf1f8;--info-text:#396792;
  --gen-bg:#f4f2fb; }
:root[data-theme="dark"] { color-scheme: dark;
  --paper:#0b0e14;--raised:#12151d;--raised-2:#1a1f2c;--line:#20242e;
  --ink:#c5c8ce;--ink-2:#7d8390;--ink-3:#5c626e;--accent:#e8eaed;--accent-fg:#0b0e14;
  --ok:#4bb489;--ok-bg:#0f1a17;--ok-text:#4bb489;--warn:#cf9d42;--warn-bg:#1c1810;--warn-text:#cf9d42;
  --crit:#e0685d;--crit-bg:#1e1210;--crit-text:#e0685d;--info:#6b9bd0;--info-bg:#0f1620;--info-text:#6b9bd0;
  --gen-bg:#14121c; }
```

- [ ] **Step 4: Create `web-next/src/styles/globals.css`**

```css
@import "./tokens.css";

* { box-sizing: border-box; }
html, body, #root { height: 100%; }
body {
  margin: 0;
  background: var(--paper);
  color: var(--ink);
  font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  font-size: 14px;
  line-height: 1.5;
  -webkit-font-smoothing: antialiased;
}
.tnum { font-variant-numeric: tabular-nums; }
:focus-visible { outline: 2px solid var(--info); outline-offset: 2px; border-radius: 4px; }
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after { animation-duration: 0.01ms !important; transition-duration: 0.01ms !important; }
}
```

- [ ] **Step 5: Run the contrast test — must pass**

Run: `cd web-next && bun test contrast`
Expected: PASS (2 tests). If any ratio < 4.5, darken the failing `-text`/`-ink-2` value until it passes.

- [ ] **Step 6: Import globals in `main.tsx`** — add as the first line:

```tsx
import "@/styles/globals.css";
```

- [ ] **Step 7: Verify build still succeeds**

Run: `cd web-next && bun run build`
Expected: PASS — Tailwind resolves the `@theme`; `dist/` emitted.

- [ ] **Step 8: Commit**

```bash
git add web-next/src/styles web-next/src/main.tsx && git commit -m "feat(web-next): quiet-instrument design tokens + a11y contrast test"
```

---

### Task 3: `cn()` helper + Button primitive

**Files:**
- Create: `web-next/src/shared/lib/cn.ts`, `web-next/src/shared/ui/button.tsx`
- Test: `web-next/src/shared/ui/button.test.tsx`

**Interfaces:**
- Produces: `cn(...inputs)` → merged className string; `<Button variant size asChild>` where `variant ∈ "solid" | "ghost" | "quiet"`, `size ∈ "sm" | "md"`.

- [ ] **Step 1: Create `cn.ts`**

```ts
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
```

- [ ] **Step 2: Write the failing Button test**

`web-next/src/shared/ui/button.test.tsx`:
```tsx
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { axe } from "jest-axe";
import { Button } from "./button";

describe("Button", () => {
  it("renders an accessible button with its label", () => {
    render(<Button>Investigate</Button>);
    expect(screen.getByRole("button", { name: "Investigate" })).toBeInTheDocument();
  });
  it("applies the ghost variant class", () => {
    render(<Button variant="ghost">Go</Button>);
    expect(screen.getByRole("button", { name: "Go" }).className).toContain("border-line");
  });
  it("has no axe violations", async () => {
    const { container } = render(<Button>Ok</Button>);
    expect(await axe(container)).toHaveNoViolations();
  });
});
```

- [ ] **Step 3: Run it — must fail**

Run: `cd web-next && bun test button`
Expected: FAIL — `./button` not found.

- [ ] **Step 4: Create `button.tsx`**

```tsx
import { Slot } from "@radix-ui/react-slot";
import { cva, type VariantProps } from "class-variance-authority";
import type { ButtonHTMLAttributes } from "react";
import { cn } from "@/shared/lib/cn";

const button = cva(
  "inline-flex items-center justify-center gap-2 rounded-[var(--radius)] font-medium transition-colors duration-200 focus-visible:outline-2 disabled:opacity-50 disabled:pointer-events-none",
  {
    variants: {
      variant: {
        solid: "bg-accent text-accent-fg hover:opacity-90",
        ghost: "border border-line bg-raised text-ink-2 hover:text-ink hover:border-ink-3",
        quiet: "text-ink-2 hover:text-ink hover:bg-raised-2",
      },
      size: { sm: "h-8 px-3 text-[12.5px]", md: "h-9 px-4 text-sm" },
    },
    defaultVariants: { variant: "solid", size: "md" },
  },
);

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> &
  VariantProps<typeof button> & { asChild?: boolean };

export function Button({ className, variant, size, asChild, ...props }: ButtonProps) {
  const Comp = asChild ? Slot : "button";
  return <Comp className={cn(button({ variant, size }), className)} {...props} />;
}
```

- [ ] **Step 5: Run the test — must pass**

Run: `cd web-next && bun test button`
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add web-next/src/shared && git commit -m "feat(web-next): cn helper + Button primitive"
```

---

### Task 4: API client (typed fetch + refresh interceptor) + overview schema

**Files:**
- Create: `web-next/src/shared/lib/api-client.ts`, `web-next/src/shared/lib/schemas/overview.ts`
- Test: `web-next/src/shared/lib/api-client.test.ts`

**Interfaces:**
- Produces:
  - `class ApiError extends Error { status: number }`
  - `setAccessToken(token: string | null): void`
  - `api<T>(path: string, schema: ZodType<T>, init?: RequestInit): Promise<T>` — attaches `Authorization: Bearer` when a token is set, `safeParse`s the body, on a 401 attempts one refresh via `POST /api/auth/refresh` then retries once (deduped), and on zod failure logs + throws `ApiError` (surfaces to Query's error state, which the UI degrades on).
  - `overviewSchema` + `type Overview` for `/api/overview`.

- [ ] **Step 1: Create `schemas/overview.ts`** (matches `internal/service/types.go` OverviewResult; hand-written now, codegen replaces in the contract plan)

```ts
import { z } from "zod";

export const overviewHealthSchema = z.object({
  score: z.number(),
  total_services: z.number(),
  by_status: z.record(z.string(), z.number()),
  throughput_per_min: z.number(),
  global_error_rate: z.number(),
  global_p95_ms: z.number(),
});

export const overviewServiceSchema = z.object({
  service: z.string(),
  status: z.string(),
  health_score: z.number(),
  requests: z.number(),
  traffic_per_min: z.number(),
  error_rate: z.number(),
  p50_ms: z.number(),
  p95_ms: z.number(),
  sparkline_traffic: z.array(z.number()).optional(),
});

export const overviewSchema = z.object({
  health: overviewHealthSchema,
  services: z.array(overviewServiceSchema),
});

export type Overview = z.infer<typeof overviewSchema>;
export type OverviewService = z.infer<typeof overviewServiceSchema>;
```

> Note for implementer: verify the field set against `internal/service/types.go`
> `OverviewResult` before finalizing; the API returns UI-required arrays always
> (never null) per the existing handler. Extra response fields are ignored by
> zod's default object parsing — fine.

- [ ] **Step 2: Write the failing api-client test**

`web-next/src/shared/lib/api-client.test.ts`:
```ts
import { describe, it, expect, vi, beforeEach } from "vitest";
import { z } from "zod";
import { api, ApiError, setAccessToken } from "./api-client";

const schema = z.object({ ok: z.boolean() });

beforeEach(() => { setAccessToken(null); vi.restoreAllMocks(); });

describe("api()", () => {
  it("parses a valid response", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ ok: true }), { status: 200 })));
    await expect(api("/x", schema)).resolves.toEqual({ ok: true });
  });

  it("throws ApiError with status on non-2xx (non-401)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("nope", { status: 500 })));
    await expect(api("/x", schema)).rejects.toMatchObject({ status: 500 } satisfies Partial<ApiError>);
  });

  it("throws ApiError when the body fails schema validation", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ ok: "yes" }), { status: 200 })));
    await expect(api("/x", schema)).rejects.toBeInstanceOf(ApiError);
  });

  it("refreshes once on 401 then retries", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      calls.push(url);
      if (url === "/api/auth/refresh") return new Response(null, { status: 200 });
      if (calls.filter((u) => u === "/x").length === 1) return new Response(null, { status: 401 });
      return new Response(JSON.stringify({ ok: true }), { status: 200 });
    }));
    await expect(api("/x", schema)).resolves.toEqual({ ok: true });
    expect(calls).toContain("/api/auth/refresh");
  });
});
```

- [ ] **Step 3: Run it — must fail**

Run: `cd web-next && bun test api-client`
Expected: FAIL — `./api-client` not found.

- [ ] **Step 4: Create `api-client.ts`**

```ts
import type { ZodType } from "zod";

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

let accessToken: string | null = null;
export function setAccessToken(token: string | null): void {
  accessToken = token;
}

// Dedupe concurrent refreshes: one in-flight refresh shared by all 401s.
let refreshInFlight: Promise<boolean> | null = null;
function refresh(): Promise<boolean> {
  refreshInFlight ??= fetch("/api/auth/refresh", { method: "POST", credentials: "include" })
    .then((r) => r.ok)
    .catch(() => false)
    .finally(() => { refreshInFlight = null; });
  return refreshInFlight;
}

function headers(init?: RequestInit): Headers {
  const h = new Headers(init?.headers);
  if (accessToken) h.set("Authorization", `Bearer ${accessToken}`);
  return h;
}

async function once<T>(path: string, schema: ZodType<T>, init?: RequestInit): Promise<Response> {
  return fetch(path, { ...init, headers: headers(init), credentials: "include" });
}

export async function api<T>(path: string, schema: ZodType<T>, init?: RequestInit): Promise<T> {
  let res = await once(path, schema, init);
  if (res.status === 401 && (await refresh())) {
    res = await once(path, schema, init);
  }
  if (!res.ok) throw new ApiError(`${path} → ${res.status}`, res.status);

  const json: unknown = await res.json().catch(() => undefined);
  const parsed = schema.safeParse(json);
  if (!parsed.success) {
    console.error(`api: schema mismatch for ${path}`, parsed.error.issues);
    throw new ApiError(`schema mismatch for ${path}`, res.status);
  }
  return parsed.data;
}
```

- [ ] **Step 5: Run the test — must pass**

Run: `cd web-next && bun test api-client`
Expected: PASS (4 tests).

- [ ] **Step 6: Commit**

```bash
git add web-next/src/shared/lib && git commit -m "feat(web-next): typed api client with refresh interceptor + zod validation"
```

---

### Task 5: Query client, namespace hook, key factory, `useOverview`

**Files:**
- Create: `web-next/src/shared/lib/query-client.ts`, `web-next/src/shared/lib/query-keys.ts`, `web-next/src/shared/hooks/use-namespace.ts`, `web-next/src/features/home/use-overview.ts`
- Test: `web-next/src/shared/lib/query-keys.test.ts`

**Interfaces:**
- Consumes: `api`, `overviewSchema`/`Overview` (Task 4).
- Produces:
  - `queryClient` with `staleTime` + `refetchInterval` defaults.
  - `keys` factory: `keys.overview(window, namespace)` → array key (namespace included; **token excluded**).
  - `useNamespace(): string` — reads `?namespace=` from the URL (empty string = default).
  - `useOverview(window): UseQueryResult<Overview>`.

- [ ] **Step 1: Write the failing key-factory test**

`web-next/src/shared/lib/query-keys.test.ts`:
```ts
import { describe, it, expect } from "vitest";
import { keys } from "./query-keys";

describe("query keys", () => {
  it("includes window and namespace, excludes any token", () => {
    expect(keys.overview(60, "prod")).toEqual(["overview", 60, "prod"]);
  });
  it("uses empty-string namespace for the default", () => {
    expect(keys.overview(60, "")).toEqual(["overview", 60, ""]);
  });
});
```

- [ ] **Step 2: Run it — must fail**

Run: `cd web-next && bun test query-keys`
Expected: FAIL — `./query-keys` not found.

- [ ] **Step 3: Create `query-keys.ts`**

```ts
// Namespace-aware key factory. The auth token is NEVER part of a key —
// auth changes invalidate queries instead (see providers).
export const keys = {
  overview: (window: number, namespace: string) => ["overview", window, namespace] as const,
};
```

- [ ] **Step 4: Run the test — must pass**

Run: `cd web-next && bun test query-keys`
Expected: PASS (2 tests).

- [ ] **Step 5: Create `query-client.ts`**

```ts
import { QueryClient } from "@tanstack/react-query";

export const REFRESH_INTERVAL = 30_000;

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      refetchInterval: REFRESH_INTERVAL,
      retry: 1,
    },
  },
});
```

- [ ] **Step 6: Create `use-namespace.ts`**

```ts
import { useSearchParams } from "react-router";

/** Namespace from the URL; empty string means the default namespace. */
export function useNamespace(): string {
  const [params] = useSearchParams();
  return params.get("namespace") ?? "";
}
```

- [ ] **Step 7: Create `features/home/use-overview.ts`**

```ts
import { useQuery } from "@tanstack/react-query";
import { api } from "@/shared/lib/api-client";
import { keys } from "@/shared/lib/query-keys";
import { useNamespace } from "@/shared/hooks/use-namespace";
import { overviewSchema } from "@/shared/lib/schemas/overview";

export function useOverview(window = 60) {
  const namespace = useNamespace();
  return useQuery({
    queryKey: keys.overview(window, namespace),
    queryFn: () => {
      const params = new URLSearchParams({ window: String(window) });
      if (namespace) params.set("namespace", namespace);
      return api(`/api/overview?${params}`, overviewSchema);
    },
  });
}
```

- [ ] **Step 8: Verify typecheck + tests**

Run: `cd web-next && bun run typecheck && bun test`
Expected: PASS — no type errors; all tests green.

- [ ] **Step 9: Commit**

```bash
git add web-next/src/shared web-next/src/features && git commit -m "feat(web-next): query client, namespace hook, key factory, useOverview"
```

---

### Task 6: App shell (providers, router, rail, states) + minimal Home

**Files:**
- Create: `web-next/src/app/theme-provider.tsx`, `web-next/src/app/providers.tsx`, `web-next/src/app/rail.tsx`, `web-next/src/app/root-layout.tsx`, `web-next/src/app/router.tsx`, `web-next/src/components/states/loading-state.tsx`, `web-next/src/components/states/error-state.tsx`, `web-next/src/components/states/empty-state.tsx`, `web-next/src/features/home/home-page.tsx`
- Modify: `web-next/src/main.tsx`
- Test: `web-next/src/features/home/home-page.test.tsx`

**Interfaces:**
- Consumes: `queryClient`, `useOverview`, `Button`.
- Produces: a mounted app — rail (Home/Services/Investigate/Alerts) + routed `<Outlet/>`; Home renders a loading state, an error state (degrades — never blank), and on success a summary line "N services healthy · X req/s · Y% errors".

- [ ] **Step 1: Create the three state primitives**

`web-next/src/components/states/loading-state.tsx`:
```tsx
export function LoadingState({ label = "Loading…" }: { label?: string }) {
  return (
    <div role="status" aria-live="polite" className="p-6 text-ink-2 text-sm">
      {label}
    </div>
  );
}
```

`web-next/src/components/states/error-state.tsx`:
```tsx
import { Button } from "@/shared/ui/button";

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div role="alert" className="p-6 flex flex-col items-start gap-3">
      <p className="text-crit-text text-sm">{message}</p>
      {onRetry && <Button variant="ghost" size="sm" onClick={onRetry}>Retry</Button>}
    </div>
  );
}
```

`web-next/src/components/states/empty-state.tsx`:
```tsx
export function EmptyState({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="p-6 flex flex-col gap-1">
      <p className="text-ink text-sm font-medium">{title}</p>
      {hint && <p className="text-ink-2 text-[12.5px]">{hint}</p>}
    </div>
  );
}
```

- [ ] **Step 2: Create `theme-provider.tsx`**

```tsx
import { createContext, useCallback, useContext, useEffect, useState } from "react";

type Theme = "light" | "dark";
const ThemeCtx = createContext<{ theme: Theme | null; toggle: () => void }>({
  theme: null,
  toggle: () => {},
});

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setTheme] = useState<Theme | null>(null);
  useEffect(() => {
    if (theme) document.documentElement.setAttribute("data-theme", theme);
    else document.documentElement.removeAttribute("data-theme");
  }, [theme]);
  const toggle = useCallback(() => {
    setTheme((t) => {
      const current = t ?? (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
      return current === "dark" ? "light" : "dark";
    });
  }, []);
  return <ThemeCtx.Provider value={{ theme, toggle }}>{children}</ThemeCtx.Provider>;
}

export function useTheme() {
  return useContext(ThemeCtx);
}
```

- [ ] **Step 3: Create `providers.tsx`**

```tsx
import { QueryClientProvider } from "@tanstack/react-query";
import { queryClient } from "@/shared/lib/query-client";
import { ThemeProvider } from "./theme-provider";

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>{children}</ThemeProvider>
    </QueryClientProvider>
  );
}
```

- [ ] **Step 4: Create `rail.tsx`** (Phosphor icons; four surfaces)

```tsx
import { NavLink } from "react-router";
import { House, StackSimple, ChatCircleText, Bell } from "@phosphor-icons/react";
import { cn } from "@/shared/lib/cn";

const items = [
  { to: "/", label: "Home", Icon: House, end: true },
  { to: "/services", label: "Services", Icon: StackSimple, end: false },
  { to: "/investigate", label: "Investigate", Icon: ChatCircleText, end: false },
  { to: "/alerts", label: "Alerts", Icon: Bell, end: false },
];

export function Rail() {
  return (
    <nav aria-label="Primary" className="w-[220px] shrink-0 border-r border-line p-3.5 flex flex-col gap-1">
      <div className="px-2 pb-5 pt-1.5 font-semibold tracking-tight">Fanout</div>
      {items.map(({ to, label, Icon, end }) => (
        <NavLink
          key={to}
          to={to}
          end={end}
          className={({ isActive }) =>
            cn(
              "flex items-center gap-3 rounded-lg px-2.5 py-2 font-medium text-ink-2 hover:bg-raised-2 hover:text-ink",
              isActive && "bg-raised-2 text-ink",
            )
          }
        >
          <Icon size={18} />
          {label}
        </NavLink>
      ))}
    </nav>
  );
}
```

- [ ] **Step 5: Create `root-layout.tsx` and `router.tsx`**

`web-next/src/app/root-layout.tsx`:
```tsx
import { Outlet } from "react-router";
import { Rail } from "./rail";

export function RootLayout() {
  return (
    <div className="flex min-h-screen">
      <Rail />
      <main className="flex-1 px-10 py-8 max-w-[1000px]">
        <Outlet />
      </main>
    </div>
  );
}
```

`web-next/src/app/router.tsx`:
```tsx
import { createBrowserRouter } from "react-router";
import { RootLayout } from "./root-layout";
import { HomePage } from "@/features/home/home-page";
import { EmptyState } from "@/components/states/empty-state";

function Placeholder({ name }: { name: string }) {
  return <EmptyState title={`${name} — coming soon`} />;
}

export const router = createBrowserRouter([
  {
    element: <RootLayout />,
    children: [
      { index: true, element: <HomePage /> },
      { path: "services", element: <Placeholder name="Services" /> },
      { path: "investigate", element: <Placeholder name="Investigate" /> },
      { path: "alerts", element: <Placeholder name="Alerts" /> },
    ],
  },
]);
```

- [ ] **Step 6: Create `features/home/home-page.tsx`** (minimal; full lifecycle is Plan 3)

```tsx
import { useOverview } from "./use-overview";
import { LoadingState } from "@/components/states/loading-state";
import { ErrorState } from "@/components/states/error-state";
import { EmptyState } from "@/components/states/empty-state";

export function HomePage() {
  const { data, isPending, isError, refetch } = useOverview();

  if (isPending) return <LoadingState label="Loading system overview…" />;
  if (isError || !data) return <ErrorState message="Couldn't load the overview." onRetry={() => void refetch()} />;
  if (data.services.length === 0)
    return <EmptyState title="Waiting for data" hint="Point OTLP at this instance to see your services." />;

  const { total_services, throughput_per_min, global_error_rate } = data.health;
  return (
    <section className="flex items-baseline gap-2">
      <span className="text-ok-text" aria-hidden>●</span>
      <h1 className="text-[22px] font-semibold tracking-tight tnum">
        {total_services} services healthy
      </h1>
      <span className="text-ink-2 tnum">
        · {Math.round(throughput_per_min / 60).toLocaleString()} req/s
        · {(global_error_rate * 100).toFixed(2)}% errors
      </span>
    </section>
  );
}
```

- [ ] **Step 7: Replace `main.tsx`**

```tsx
import "@/styles/globals.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router";
import { Providers } from "@/app/providers";
import { router } from "@/app/router";

const root = document.getElementById("root");
if (!root) throw new Error("root element missing");
createRoot(root).render(
  <StrictMode>
    <Providers>
      <RouterProvider router={router} />
    </Providers>
  </StrictMode>,
);
```

- [ ] **Step 8: Write the Home test** (loading, error-degrades-not-blank, success)

`web-next/src/features/home/home-page.test.tsx`:
```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { HomePage } from "./home-page";

function renderHome() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><HomePage /></MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => vi.restoreAllMocks());

describe("HomePage", () => {
  it("shows the summary line on success", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      health: { score: 1, total_services: 12, by_status: { healthy: 12 }, throughput_per_min: 2712000, global_error_rate: 0.0021, global_p95_ms: 142 },
      services: [{ service: "checkout", status: "healthy", health_score: 1, requests: 10, traffic_per_min: 60, error_rate: 0, p50_ms: 1, p95_ms: 2 }],
    }), { status: 200 })));
    renderHome();
    expect(await screen.findByRole("heading", { name: /12 services healthy/ })).toBeInTheDocument();
  });

  it("degrades to an error message (never blank) on failure", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("boom", { status: 500 })));
    renderHome();
    expect(await screen.findByRole("alert")).toHaveTextContent(/couldn't load/i);
  });
});
```

- [ ] **Step 9: Run tests + typecheck + build**

Run: `cd web-next && bun test && bun run typecheck && bun run build`
Expected: PASS — all tests green, no type errors, `dist/` emitted.

- [ ] **Step 10: Commit**

```bash
git add web-next/src && git commit -m "feat(web-next): app shell (rail, router, providers, states) + minimal overview Home"
```

---

### Task 7: Serve `web-next` from the Go binary behind a flag

**Files:**
- Create: `internal/uinext/uinext.go`, `internal/uinext/dist/.gitkeep`
- Modify: `cmd/fanout/main.go` (SPA registration block, ~line 276), `justfile` (add `build-next`)

**Interfaces:**
- Consumes: existing `internal/ui` pattern (`Dist()`, `RegisterSPARoutes`).
- Produces: when `FANOUT_UI_NEXT=1`, the binary serves `internal/uinext/dist`; otherwise the current `internal/ui/dist`. `just build-next` copies `web-next/dist` into `internal/uinext/dist`.

- [ ] **Step 1: Create `internal/uinext/dist/.gitkeep`** (empty file, so `//go:embed` compiles before a build)

```
```

- [ ] **Step 2: Create `internal/uinext/uinext.go`** (mirror of `internal/ui`, different embed root)

```go
// Package uinext embeds the clean-rewrite React UI (web-next) and serves it
// as a SPA. Selected at runtime by FANOUT_UI_NEXT; the default remains
// internal/ui until web-next reaches parity and the dirs are swapped.
package uinext

import (
	"fmt"
	"io/fs"

	"embed"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/ui"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the embedded web-next filesystem rooted at dist, or an error
// if it was built without running the web-next build first.
func Dist() (fs.FS, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("web-next UI not built: run `just build-next`: %w", err)
	}
	return sub, nil
}

// RegisterSPARoutes delegates to the shared SPA handler in internal/ui so the
// serve/catch-all behavior stays identical between the two UIs.
func RegisterSPARoutes(e *echo.Echo, spaFS fs.FS) {
	ui.RegisterSPARoutes(e, spaFS)
}
```

> Implementer note: confirm `ui.RegisterSPARoutes` is exported (it is, per
> `internal/ui/ui.go`). If Go complains about an unused import ordering, run
> `gofmt -w internal/uinext/uinext.go`.

- [ ] **Step 3: Write a Go test that unbuilt dist errors cleanly**

`internal/uinext/uinext_test.go`:
```go
package uinext

import "testing"

// With only the .gitkeep placeholder embedded, Dist must return an error
// (not panic, not serve 404s) so main can log-and-skip.
func TestDistUnbuiltReturnsError(t *testing.T) {
	if _, err := Dist(); err == nil {
		t.Fatal("expected error when web-next dist has no index.html")
	}
}
```

- [ ] **Step 4: Run the Go test — expect PASS**

Run: `go test ./internal/uinext/`
Expected: PASS (dist has only `.gitkeep`, so `Dist()` errors as designed).

- [ ] **Step 5: Modify `cmd/fanout/main.go`** — replace the SPA registration block (~line 276)

Find:
```go
	// SPA catch-all — serves the embedded React app for any unmatched route.
	// API routes registered above take priority; everything else falls through here.
	spaFS, spaErr := ui.Dist()
	if spaErr != nil {
		slog.Warn("React SPA not available (not built?)", "err", spaErr)
	} else {
		ui.RegisterSPARoutes(e, spaFS)
		slog.Info("React SPA enabled", "path", "/*")
	}
```

Replace with (adds `uinext` import at top of file too):
```go
	// SPA catch-all — serves the embedded React app for any unmatched route.
	// FANOUT_UI_NEXT=1 selects the clean-rewrite UI (web-next); default is the
	// current UI. API routes registered above take priority.
	var (
		spaFS  fs.FS
		spaErr error
		uiName = "react-spa"
	)
	if os.Getenv("FANOUT_UI_NEXT") == "1" {
		spaFS, spaErr = uinext.Dist()
		uiName = "react-spa-next"
	} else {
		spaFS, spaErr = ui.Dist()
	}
	if spaErr != nil {
		slog.Warn("SPA not available (not built?)", "ui", uiName, "err", spaErr)
	} else {
		if uiName == "react-spa-next" {
			uinext.RegisterSPARoutes(e, spaFS)
		} else {
			ui.RegisterSPARoutes(e, spaFS)
		}
		slog.Info("SPA enabled", "ui", uiName, "path", "/*")
	}
```

> Implementer note: ensure `"io/fs"`, `"os"`, and
> `"github.com/labstack/fanout/internal/uinext"` are imported. `gofmt -w` and
> `go vet ./cmd/fanout/`.

- [ ] **Step 6: Add the `build-next` target to `justfile`** (after the `build` target)

```make
# Build production binary serving the clean-rewrite UI (web-next) behind the flag.
build-next VERSION=`git describe --tags --always --dirty 2>/dev/null || echo dev`:
    cd web-next && bun install && bun run build
    rm -rf internal/uinext/dist/*
    cp -r web-next/dist/* internal/uinext/dist/
    go build -ldflags "-s -w -X main.version={{VERSION}}" -o {{bin}} ./cmd/fanout
```

- [ ] **Step 7: Build the binary with web-next and smoke-test the served path**

Run:
```bash
just build-next
FANOUT_UI_NEXT=1 ./bin/fanout &
sleep 2
curl -sf http://localhost:7520/ | grep -q '<div id="root">' && echo SERVED_OK
kill %1
```
Expected: `SERVED_OK` — the binary serves web-next's `index.html` at `/`.

- [ ] **Step 8: Verify the default UI is unaffected**

Run: `just build && ./bin/fanout &` then `curl -sf http://localhost:7520/ | head -c 100; kill %1`
Expected: the original UI's HTML (unchanged default path).

- [ ] **Step 9: Commit**

```bash
git add internal/uinext cmd/fanout/main.go justfile && git commit -m "feat(build): serve web-next from the binary behind FANOUT_UI_NEXT flag"
```

---

### Task 8: Wire the web-next quality gate into `just check`

**Files:**
- Modify: `justfile` (extend the `check` recipe to run web-next lint+typecheck+test)

**Interfaces:**
- Produces: `just check` fails if web-next lint, typecheck, or tests fail.

- [ ] **Step 1: Inspect the current `check` recipe**

Run: `grep -n -A8 '^check' justfile`
Expected: shows the existing pre-commit + CI recipe (Go + web steps).

- [ ] **Step 2: Add web-next steps to the `check` recipe** — append these lines inside the recipe body (match the existing indentation and command style; adjust to how `web` is invoked there):

```make
    cd web-next && bun run lint
    cd web-next && bun run typecheck
    cd web-next && bun test
```

- [ ] **Step 3: Run the gate**

Run: `just check`
Expected: PASS — Go checks plus web-next lint/typecheck/test all green.

- [ ] **Step 4: Commit**

```bash
git add justfile && git commit -m "chore(ci): run web-next lint/typecheck/test in just check"
```

---

## Self-Review

**Spec coverage (this slice = spec §9 steps 0–1):**
- Scaffold + TS strict + `noUncheckedIndexedAccess` + eslint(+a11y) + vitest/axe → Task 1. ✓
- Tokens with a11y-fixed ramps (ink-3 non-text, status `-text` ramp) + contrast test → Task 2. ✓
- `cn` + a primitive (Button; more primitives arrive with the surfaces that need them — YAGNI) → Task 3. ✓
- Data layer: typed fetch + refresh interceptor + zod `safeParse` + ApiError → Task 4. ✓
- Query client + `useNamespace` + query-key factory (token excluded) → Task 5. ✓
- App shell (rail, router, providers, states primitives) + minimal overview-backed Home → Task 6. ✓
- Embed behind a flag; exercised via the real served-binary path → Task 7. ✓
- Quality gate wired into `just check` → Task 8. ✓
- **Deferred to later plans (correctly out of scope here):** genblocks→zod codegen (its own plan, before Home lifecycle); full Home lifecycle/incident cards; Services/Alerts/Investigate/blocks; auth/public-read/settings; supporting surfaces; parity checklist + cutover.

**Placeholder scan:** No "TBD"/"add error handling"/"similar to Task N". Two `> Implementer note:` blocks give verification steps, not missing code. `.gitkeep` and `internal/uinext/dist/.gitkeep` are intentionally empty files.

**Type consistency:** `api(path, schema, init)` signature identical in Tasks 4–6; `overviewSchema`/`Overview` names consistent; `keys.overview(window, namespace)` used identically in Tasks 5–6; `setAccessToken`/`ApiError` names stable; `Button` variant names (`solid`/`ghost`/`quiet`) consistent between Task 3 and Task 6 usage.

**Known verification points for the implementer (not placeholders — explicit checks):**
- Confirm `overviewSchema` fields against `internal/service/types.go` `OverviewResult` (Task 4 Step 1 note).
- Confirm exact library versions resolve in this repo's registry; if a pinned version is unavailable, take the nearest compatible and note it in the commit.
- Confirm the `just check` recipe's existing web invocation style before appending (Task 8 Step 2).
