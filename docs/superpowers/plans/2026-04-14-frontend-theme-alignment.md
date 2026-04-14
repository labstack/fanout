# Frontend Theme & Stack Alignment Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align Fanout's frontend with Monk's theme system (dark-only, surface tiers, Fragment Mono body, blue accent), layout patterns (RootLayout shell, Nav, Footer), and data-fetching stack (React Query), while preserving Fanout's chat-first UX and ECharts visualizations.

**Architecture:** Replace the current light+dark OKLCH theme with a dark-only hex-based surface tier system matching Monk's structure. Add a proper layout shell around the existing chat page. Introduce React Query for non-streaming API calls. Keep Zustand for SSE chat state.

**Tech Stack:** React 19, React Router 7, Tailwind 4, Radix UI, Zustand (chat), TanStack React Query 5 (new), Sonner (new), ECharts, D3

---

## File Structure

### New Files
- `client/src/lib/theme.ts` — JS color/font constants for ECharts and imperative JS
- `client/src/components/layout/root-layout.tsx` — Global layout shell (Nav + main + Footer + Toaster)
- `client/src/components/layout/nav.tsx` — Sticky header navigation
- `client/src/components/layout/footer.tsx` — Footer with copyright
- `client/src/components/layout/nav-loader.tsx` — Top progress bar on route transitions
- `client/src/api/client.ts` — Fetch wrapper with Bearer auth

### Modified Files
- `client/index.html` — Add Fragment Mono font, update favicon color, meta tags
- `client/package.json` — Add `@tanstack/react-query`, `sonner`
- `client/src/index.css` — Complete theme overhaul: dark-only surface tiers, blue accent, custom utilities, noise overlay, animations
- `client/src/main.tsx` — Add QueryClientProvider
- `client/src/App.tsx` — Wrap routes in RootLayout
- `client/src/lib/echarts.ts` — Update status colors + tooltip/axis helpers to use new theme tokens
- `client/src/components/chat/ChatMessage.tsx` — Replace hardcoded `#818cf8` with accent color
- `client/src/components/chat/ChatInput.tsx` — Update input glow to use accent color
- `client/src/components/chat/EmptyState.tsx` — Minor token updates
- `client/src/components/chat/ToolStatus.tsx` — Use healthy color instead of hardcoded emerald
- `client/src/components/blocks/MetricsBlock.tsx` — Use theme constants for status colors
- `client/src/components/blocks/TimeseriesBlock.tsx` — Update default chart colors
- `client/src/components/blocks/BarBlock.tsx` — Update default color
- `client/src/components/blocks/TopologyBlock.tsx` — Use theme constants for status colors
- `client/src/components/blocks/CorrelationBlock.tsx` — Use theme constants
- `client/src/components/blocks/TraceWaterfallBlock.tsx` — Use theme constants for error color
- `client/src/components/blocks/DepMatrixBlock.tsx` — Use theme constants
- `client/src/components/blocks/HeatmapBlock.tsx` — Update heatmap color range
- `client/src/components/blocks/LogsBlock.tsx` — Replace hardcoded border color
- `client/src/pages/ChatPage.tsx` — Remove inline header (now in Nav), adjust layout
- `client/src/pages/DemoPage.tsx` — Update demo data colors
- `client/src/stores/chat.ts` — No changes (SSE stays on Zustand)

---

### Task 1: Install Dependencies

**Files:**
- Modify: `client/package.json`

- [ ] **Step 1: Install new packages**

```bash
cd /Users/v/Projects/labstack/fanout/client && npm install @tanstack/react-query sonner
```

- [ ] **Step 2: Verify install succeeded**

```bash
cd /Users/v/Projects/labstack/fanout/client && npm ls @tanstack/react-query sonner
```

Expected: Both packages listed with versions, no peer dep errors.

- [ ] **Step 3: Commit**

```bash
git add client/package.json client/package-lock.json
git commit -m "chore: add react-query and sonner dependencies"
```

---

### Task 2: Update Font Loading

**Files:**
- Modify: `client/index.html`

- [ ] **Step 1: Add Fragment Mono to Google Fonts request and update favicon accent to blue**

Replace the current `index.html` content with:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="theme-color" content="#09090b" />
    <link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'%3E%3Ccircle cx='16' cy='16' r='14' fill='none' stroke='%233b82f6' stroke-width='2.5' opacity='0.3'/%3E%3Ccircle cx='16' cy='16' r='8' fill='none' stroke='%233b82f6' stroke-width='2.5' opacity='0.6'/%3E%3Ccircle cx='16' cy='16' r='3' fill='%233b82f6'/%3E%3C/svg%3E" />
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link href="https://fonts.googleapis.com/css2?family=DM+Sans:ital,wght@0,400;0,500;0,600;0,700;0,800;1,400;1,500;1,600;1,700&family=Fragment+Mono:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;600;700&display=swap" rel="stylesheet" />
    <title>Fanout</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

Key changes:
- Removed `class="dark"` from html (we'll make dark the only mode via CSS)
- Added Fragment Mono font family
- Updated favicon stroke from `%2306b6d4` (cyan) to `%233b82f6` (blue-500)
- Updated theme-color to `#09090b` (surface color)
- Added more DM Sans weights

- [ ] **Step 2: Verify fonts load in browser**

Run: `cd /Users/v/Projects/labstack/fanout && just up` (or `cd client && npm run dev`)

Open browser, inspect body computed font — should include Fragment Mono in the font list after CSS changes.

- [ ] **Step 3: Commit**

```bash
git add client/index.html
git commit -m "chore: add Fragment Mono font, update favicon to blue accent"
```

---

### Task 3: Complete Theme Overhaul (index.css)

**Files:**
- Modify: `client/src/index.css`

- [ ] **Step 1: Replace the entire index.css with the new dark-only theme**

```css
@import "tailwindcss";
@plugin "@tailwindcss/typography";
@import "tw-animate-css";
@import "shadcn/tailwind.css";

/* ─── Theme tokens ─────────────────────────────────────────────────────── */
@theme inline {
    --font-sans: "Fragment Mono", monospace;
    --font-heading: "DM Sans", system-ui, -apple-system, sans-serif;
    --font-mono: "JetBrains Mono", "SF Mono", ui-monospace, monospace;
    --radius-sm: calc(var(--radius) - 4px);
    --radius-md: calc(var(--radius) - 2px);
    --radius-lg: var(--radius);
    --radius-xl: calc(var(--radius) + 4px);
    --radius-2xl: calc(var(--radius) + 8px);
    --radius-3xl: calc(var(--radius) + 12px);
    --radius-4xl: calc(var(--radius) + 16px);
    --color-surface: var(--surface);
    --color-surface-1: var(--surface-1);
    --color-surface-2: var(--surface-2);
    --color-surface-3: var(--surface-3);
    --color-background: var(--background);
    --color-foreground: var(--foreground);
    --color-card: var(--card);
    --color-card-foreground: var(--card-foreground);
    --color-popover: var(--popover);
    --color-popover-foreground: var(--popover-foreground);
    --color-primary: var(--primary);
    --color-primary-foreground: var(--primary-foreground);
    --color-secondary: var(--secondary);
    --color-secondary-foreground: var(--secondary-foreground);
    --color-muted: var(--muted);
    --color-muted-foreground: var(--muted-foreground);
    --color-accent: var(--accent);
    --color-accent-foreground: var(--accent-foreground);
    --color-destructive: var(--destructive);
    --color-border: var(--border);
    --color-input: var(--input);
    --color-ring: var(--ring);
    --color-chart-1: var(--chart-1);
    --color-chart-2: var(--chart-2);
    --color-chart-3: var(--chart-3);
    --color-chart-4: var(--chart-4);
    --color-chart-5: var(--chart-5);
    --color-healthy: var(--healthy);
    --color-degraded: var(--degraded);
    --color-unhealthy: var(--unhealthy);
}

/* ─── CSS Houdini for animated gradient border ─────────────────────────── */
@property --border-angle {
  syntax: "<angle>";
  inherits: false;
  initial-value: 0deg;
}

/* ─── Dark-only theme ─────────────────────────────────────────────────── */
:root {
    color-scheme: dark;
    --radius: 0.625rem;

    /* Surface tiers */
    --surface:   #09090b;
    --surface-1: #121215;
    --surface-2: #1a1a1f;
    --surface-3: #252529;

    /* Semantic colors */
    --background: #09090b;
    --foreground: #e4e4e7;
    --card: #121215;
    --card-foreground: #e4e4e7;
    --popover: #1a1a1f;
    --popover-foreground: #e4e4e7;
    --primary: #60a5fa;
    --primary-foreground: #09090b;
    --secondary: #1a1a1f;
    --secondary-foreground: #d4d4d8;
    --muted: #1a1a1f;
    --muted-foreground: #71717a;
    --accent: #1a1a1f;
    --accent-foreground: #e4e4e7;
    --destructive: #f87171;
    --border: #2a2a30;
    --input: #2a2a30;
    --ring: #60a5fa;

    /* Chart palette */
    --chart-1: #60a5fa;
    --chart-2: #34d399;
    --chart-3: #fbbf24;
    --chart-4: #a78bfa;
    --chart-5: #fb923c;

    /* Status colors */
    --healthy: #34d399;
    --degraded: #fbbf24;
    --unhealthy: #f87171;
}

/* ─── Base ─────────────────────────────────────────────────────────────── */
@layer base {
  * {
    @apply border-border outline-ring/50;
  }
  body {
    background: var(--surface);
    color: var(--foreground);
    font-size: 16px;
    line-height: 1.5;
    -webkit-font-smoothing: antialiased;
  }
  h1, h2, h3, h4, h5, h6 {
    font-family: var(--font-heading);
  }
  button, [role="menuitem"], a, summary {
    outline: none;
  }
}

/* ─── Noise overlay ───────────────────────────────────────────────────── */
.noise::before {
  content: "";
  position: fixed;
  inset: 0;
  pointer-events: none;
  opacity: 0.015;
  background-image: url("data:image/svg+xml,%3Csvg viewBox='0 0 256 256' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.9' numOctaves='4' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E");
  z-index: 1;
}

/* ─── Tables ──────────────────────────────────────────────────────────── */
@layer base {
  table { border-collapse: collapse; }
  th {
    font-family: var(--font-mono);
    font-size: 0.8125rem;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--muted-foreground);
    border-bottom: 1px solid var(--border);
  }
  tr:hover td {
    background: rgba(255, 255, 255, 0.02);
  }
}

/* ─── Block card ──────────────────────────────────────────────────────── */
.block-card,
.block-card-flush {
  background: var(--surface-1);
  border: 1px solid rgba(96, 165, 250, 0.15);
  border-radius: 14px;
}

.block-card {
  padding: 1rem;
}

.block-title {
  margin-bottom: 0.5rem;
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.1em;
  color: #60a5fa;
}

/* ─── Prose theme (shared between chat markdown and text blocks) ──────── */
.prose-themed {
  @apply prose prose-invert max-w-none;
  @apply prose-a:text-[#60a5fa] prose-a:no-underline hover:prose-a:underline;
  @apply prose-code:font-mono prose-code:text-[#93c5fd] prose-code:bg-[#60a5fa]/10;
  @apply prose-code:px-1.5 prose-code:py-0.5 prose-code:rounded-md prose-code:text-sm;
  @apply prose-code:before:content-none prose-code:after:content-none;
  @apply prose-pre:bg-[#121215] prose-pre:border prose-pre:border-[#60a5fa]/15 prose-pre:rounded-[14px];
  @apply prose-blockquote:border-l-[#60a5fa] prose-blockquote:border-l-2;
}

/* ─── Custom utilities (Monk-aligned) ─────────────────────────────────── */
@utility mono {
  font-family: var(--font-mono);
}

@utility detail-label {
  font-family: var(--font-mono);
  font-size: 0.6875rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.14em;
  color: #52525b;
}

@utility stat-card {
  background: var(--surface-2);
  border-radius: 0.5rem;
  padding: 1.25rem;
}

/* ─── Buttons ─────────────────────────────────────────────────────────── */
@utility btn-primary {
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 0.5rem;
  background: #3b82f6;
  padding: 0.625rem 1.5rem;
  font-size: 0.875rem;
  font-weight: 600;
  color: white;
  transition: all 0.2s;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.3), 0 0 0 1px rgba(96, 165, 250, 0.1);
  &:hover {
    background: #60a5fa;
    box-shadow: 0 4px 16px -4px rgba(96, 165, 250, 0.35);
    transform: translateY(-1px);
  }
  &:active { transform: translateY(0); }
}

@utility btn-ghost {
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--border);
  border-radius: 0.5rem;
  padding: 0.5rem 1rem;
  font-size: 0.875rem;
  font-weight: 500;
  color: #a1a1aa;
  transition: all 0.2s;
  &:hover {
    border-color: #3f3f46;
    color: #e4e4e7;
    background: rgba(255, 255, 255, 0.02);
  }
}

/* ─── Input ───────────────────────────────────────────────────────────── */
@utility input-field {
  width: 100%;
  border-radius: 0.5rem;
  border: 1px solid var(--border);
  background: var(--surface-1);
  padding: 0.625rem 0.75rem;
  font-size: 0.875rem;
  color: #e4e4e7;
  transition: border-color 0.2s, box-shadow 0.2s;
  &::placeholder { color: #52525b; }
  &:focus {
    border-color: #3b82f6;
    box-shadow: 0 0 0 3px rgba(96, 165, 250, 0.08);
    outline: none;
  }
}

/* ─── Dropdown ────────────────────────────────────────────────────────── */
@utility dropdown-content {
  min-width: 160px;
  border-radius: 0.5rem;
  border: 1px solid var(--border);
  background: var(--surface-1);
  padding: 0.25rem;
  box-shadow: 0 16px 48px -12px rgba(0, 0, 0, 0.6);
  z-index: 50;
  animation: dropdownIn 0.15s ease-out;
}

@utility dropdown-item {
  display: flex;
  cursor: pointer;
  user-select: none;
  align-items: center;
  border-radius: 0.25rem;
  padding: 0.375rem 0.625rem;
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  color: #d4d4d8;
  transition: all 0.1s;
  &:hover, &[data-highlighted] {
    background: var(--surface-2);
    color: #e4e4e7;
  }
}

/* ─── Animations ───────────────────────────────────────────────────────── */
@keyframes fade-up {
  from { opacity: 0; transform: translateY(8px); }
  to   { opacity: 1; transform: translateY(0); }
}

@keyframes fadeUp {
  from { opacity: 0; transform: translateY(12px); }
  to   { opacity: 1; transform: translateY(0); }
}

@keyframes revealUp {
  from { opacity: 0; transform: translateY(10px); }
  to   { opacity: 1; transform: translateY(0); }
}

@keyframes fadeIn {
  from { opacity: 0; }
  to   { opacity: 1; }
}

@keyframes dropdownIn {
  from { opacity: 0; transform: scale(0.97) translateY(-4px); }
  to   { opacity: 1; transform: scale(1) translateY(0); }
}

@keyframes border-rotate {
  to { --border-angle: 360deg; }
}

@keyframes scale-dot {
  0%, 100% { transform: scale(0.6); opacity: 0.4; }
  50% { transform: scale(1.2); opacity: 1; }
}

@keyframes shimmer {
  0% { background-position: -200% 0; }
  100% { background-position: 200% 0; }
}

@keyframes opacity-pulse {
  0%, 100% { opacity: 0.5; }
  50% { opacity: 1; }
}

@keyframes navLoad {
  0% { transform: translateX(-100%); }
  100% { transform: translateX(433%); }
}

.animate-fade-up {
  animation: fade-up 0.3s ease-out both;
}

@utility fade-up {
  animation: fadeUp 0.5s cubic-bezier(0.16, 1, 0.3, 1) both;
}

@utility reveal {
  opacity: 0;
  animation: revealUp 0.5s cubic-bezier(0.16, 1, 0.3, 1) forwards;
}

@utility fade-in {
  animation: fadeIn 0.4s ease-out both;
}

/* ─── Shimmer skeleton ────────────────────────────────────────────────── */
@utility shimmer {
  background: linear-gradient(90deg, var(--surface-2) 25%, var(--surface-3) 50%, var(--surface-2) 75%);
  background-size: 200% 100%;
  animation: shimmer 1.5s infinite;
  border-radius: 4px;
}

.shimmer-text {
  animation: opacity-pulse 3s ease-in-out infinite;
}

/* ─── Animated gradient border for input ───────────────────────────────── */
.input-glow {
  position: relative;
}

.input-glow::before {
  content: "";
  position: absolute;
  inset: -1px;
  border-radius: 1rem;
  padding: 1px;
  background: conic-gradient(
    from var(--border-angle),
    transparent 30%,
    var(--primary) 50%,
    transparent 70%
  );
  -webkit-mask:
    linear-gradient(#fff 0 0) content-box,
    linear-gradient(#fff 0 0);
  -webkit-mask-composite: xor;
  mask:
    linear-gradient(#fff 0 0) content-box,
    linear-gradient(#fff 0 0);
  mask-composite: exclude;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.4s ease;
}

.input-glow:focus-within::before {
  animation: border-rotate 4s linear infinite;
  opacity: 0.5;
}

/* ─── Nav loader ──────────────────────────────────────────────────────── */
.nav-loader {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  height: 2px;
  z-index: 9999;
  overflow: hidden;
  opacity: 0;
  pointer-events: none;
  transition: opacity 0.2s;
}
.nav-loader.active { opacity: 1; }
.nav-loader-bar {
  height: 100%;
  width: 30%;
  background: var(--primary);
  box-shadow: 0 0 12px var(--primary), 0 0 30px rgba(96, 165, 250, 0.3);
  animation: navLoad 1.2s ease-in-out infinite;
}

/* ─── Scrollbar ────────────────────────────────────────────────────────── */
::-webkit-scrollbar { width: 6px; height: 6px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: var(--surface-3); border-radius: 3px; }
::-webkit-scrollbar-thumb:hover { background: #3f3f46; }

/* ─── Autofill override ───────────────────────────────────────────────── */
input:-webkit-autofill,
input:-webkit-autofill:hover,
input:-webkit-autofill:focus {
  -webkit-box-shadow: 0 0 0 1000px var(--surface-1) inset;
  -webkit-text-fill-color: #e4e4e7;
  caret-color: #e4e4e7;
}
```

- [ ] **Step 2: Verify the dev server renders correctly**

Run: `cd /Users/v/Projects/labstack/fanout/client && npm run dev`

Open `https://fanout.test:7521` — background should be `#09090b`, text should be light zinc, body font should be Fragment Mono. The chat page may have some styling issues that we fix in later tasks.

- [ ] **Step 3: Commit**

```bash
git add client/src/index.css
git commit -m "feat: dark-only theme with surface tiers, blue accent, Monk-aligned utilities"
```

---

### Task 4: Add Theme Constants for JS

**Files:**
- Create: `client/src/lib/theme.ts`

- [ ] **Step 1: Create theme.ts with color and font constants**

```typescript
export const COLORS = {
  surface: "#09090b",
  surface1: "#121215",
  surface2: "#1a1a1f",
  surface3: "#252529",
  border: "#2a2a30",
  accent: "#60a5fa",
  accentDim: "#3b82f6",
  healthy: "#34d399",
  degraded: "#fbbf24",
  unhealthy: "#f87171",
  grid: "rgba(255,255,255,0.03)",
  label: "#71717a",
  text: "#e4e4e7",
  textDim: "#71717a",
} as const;

export const FONTS = {
  mono: "'JetBrains Mono', monospace",
  sans: "'Fragment Mono', monospace",
  heading: "'DM Sans', system-ui, sans-serif",
} as const;
```

- [ ] **Step 2: Commit**

```bash
git add client/src/lib/theme.ts
git commit -m "feat: add JS theme constants for ECharts and imperative code"
```

---

### Task 5: Update ECharts Helpers

**Files:**
- Modify: `client/src/lib/echarts.ts`

- [ ] **Step 1: Update statusColor to use COLORS constants**

Replace the `statusColor` function and imports:

Add this import at the top of the file:
```typescript
import { COLORS } from "./theme";
```

Replace the `statusColor` function:
```typescript
/** Status color used by topology and sankey blocks. */
export function statusColor(status?: string): string {
  switch (status?.toLowerCase()) {
    case "healthy": return COLORS.healthy;
    case "degraded": return COLORS.degraded;
    case "unhealthy": return COLORS.unhealthy;
    default: return COLORS.label;
  }
}
```

- [ ] **Step 2: Verify build compiles**

```bash
cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit
```

Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add client/src/lib/echarts.ts
git commit -m "refactor: use theme constants in ECharts helpers"
```

---

### Task 6: Update Block Components (Hardcoded Colors)

**Files:**
- Modify: `client/src/components/blocks/MetricsBlock.tsx`
- Modify: `client/src/components/blocks/TimeseriesBlock.tsx`
- Modify: `client/src/components/blocks/BarBlock.tsx`
- Modify: `client/src/components/blocks/TopologyBlock.tsx`
- Modify: `client/src/components/blocks/CorrelationBlock.tsx`
- Modify: `client/src/components/blocks/TraceWaterfallBlock.tsx`
- Modify: `client/src/components/blocks/DepMatrixBlock.tsx`
- Modify: `client/src/components/blocks/HeatmapBlock.tsx`
- Modify: `client/src/components/blocks/LogsBlock.tsx`

- [ ] **Step 1: Update MetricsBlock.tsx**

Replace the hardcoded color map at the top:
```typescript
import { COLORS } from "@/lib/theme";

const STATUS_COLOR: Record<string, string> = {
  ok: COLORS.healthy,
  warning: COLORS.degraded,
  danger: COLORS.unhealthy,
};

const DEFAULT_ACCENT = COLORS.accent;
```

- [ ] **Step 2: Update TimeseriesBlock.tsx**

Replace the DEFAULT_COLORS constant:
```typescript
import { COLORS } from "@/lib/theme";

const DEFAULT_COLORS = [COLORS.accent, COLORS.healthy, COLORS.degraded, "#fb923c", "#a78bfa"];
```

- [ ] **Step 3: Update BarBlock.tsx**

Replace the DEFAULT_COLOR constant:
```typescript
import { COLORS } from "@/lib/theme";

const DEFAULT_COLOR = COLORS.accent;
```

- [ ] **Step 4: Update TopologyBlock.tsx**

Replace hardcoded status colors. Add import:
```typescript
import { COLORS } from "@/lib/theme";
```

In the edge color line (approx line 86), replace:
```typescript
color: e.errorRate > 3 ? COLORS.unhealthy : e.errorRate > 1 ? COLORS.degraded : cssVar("--border"),
```

In the legend (approx line 116-118), replace:
```typescript
{ color: COLORS.healthy, label: "Healthy" },
{ color: COLORS.degraded, label: "Degraded" },
{ color: COLORS.unhealthy, label: "Unhealthy" },
```

- [ ] **Step 5: Update CorrelationBlock.tsx**

Replace hardcoded `#ef4444` and `#f59e0b` with `COLORS.unhealthy` and `COLORS.degraded`. Add import:
```typescript
import { COLORS } from "@/lib/theme";
```

Replace the color references (approx line 60 and 71):
```typescript
const color = m.severity === "critical" ? COLORS.unhealthy : COLORS.degraded;
```

- [ ] **Step 6: Update TraceWaterfallBlock.tsx**

Replace hardcoded `#ef4444` with `COLORS.unhealthy`. Add import:
```typescript
import { COLORS } from "@/lib/theme";
```

Replace all three occurrences of `"#ef4444"` with `COLORS.unhealthy`.

- [ ] **Step 7: Update DepMatrixBlock.tsx**

Replace the color functions and legend. Add import:
```typescript
import { COLORS } from "@/lib/theme";
```

Replace the `bgColor` function to use COLORS:
```typescript
function bgColor(errorRate: number): string {
  if (errorRate === 0) return `${COLORS.healthy}26`;     // 15% opacity
  if (errorRate < 0.5) return `${COLORS.healthy}4d`;     // 30% opacity
  if (errorRate < 1.0) return `${COLORS.degraded}4d`;    // 30% opacity
  if (errorRate < 2.0) return `${COLORS.degraded}99`;    // 60% opacity
  return `${COLORS.unhealthy}99`;                        // 60% opacity
}

function textColor(errorRate: number): string {
  if (errorRate > 1) return COLORS.unhealthy;
  if (errorRate > 0.5) return COLORS.degraded;
  return COLORS.healthy;
}
```

Update legend colors to match.

- [ ] **Step 8: Update HeatmapBlock.tsx**

Replace the `inRange` colors (line 47) to use a blue-based heat range:
```typescript
inRange: { color: [COLORS.surface2, COLORS.accent, COLORS.unhealthy] },
```

Add import:
```typescript
import { COLORS } from "@/lib/theme";
```

- [ ] **Step 9: Update LogsBlock.tsx**

Replace `border-[#818cf8]/10` with `border-primary/10`:
```tsx
<div className="flex items-center justify-between px-4 py-3 border-b border-primary/10">
```

- [ ] **Step 10: Verify build compiles**

```bash
cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit
```

Expected: No errors.

- [ ] **Step 11: Commit**

```bash
git add client/src/components/blocks/
git commit -m "refactor: replace hardcoded colors in block components with theme constants"
```

---

### Task 7: Update Chat Components

**Files:**
- Modify: `client/src/components/chat/ChatMessage.tsx`
- Modify: `client/src/components/chat/ToolStatus.tsx`

- [ ] **Step 1: Update ChatMessage.tsx — replace all `#818cf8` with blue accent**

Replace `bg-[#818cf8]` with `bg-primary`:
```tsx
<div className="bg-primary text-primary-foreground rounded-[16px] rounded-br-[4px] px-4 py-2.5 max-w-[80%] text-base leading-relaxed">
```

Replace `bg-[#818cf8]/15` with `bg-primary/15`:
```tsx
<div className="shrink-0 mt-0.5 flex items-center justify-center h-7 w-7 rounded-lg bg-primary/15">
```

Replace `text-[#818cf8]` with `text-primary`:
```tsx
<Radio className="h-3.5 w-3.5 text-primary" />
```

- [ ] **Step 2: Update ToolStatus.tsx — replace hardcoded emerald with semantic healthy colors**

Replace the completed badge classes:
```tsx
// Before: bg-emerald-500/10 border-emerald-500/20 text-emerald-400
// After:  bg-healthy/10 border-healthy/20 text-healthy
```

This uses the Tailwind theme tokens `--color-healthy` we defined in index.css.

- [ ] **Step 3: Verify build compiles**

```bash
cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit
```

- [ ] **Step 4: Commit**

```bash
git add client/src/components/chat/
git commit -m "refactor: use theme tokens in chat components, replace hardcoded indigo with blue accent"
```

---

### Task 8: Update DemoPage Colors

**Files:**
- Modify: `client/src/pages/DemoPage.tsx`

- [ ] **Step 1: Replace hardcoded demo data colors**

Replace `#8884d8` with `#60a5fa` (accent), `#ef4444` with `#f87171` (unhealthy), `#82ca9d` with `#34d399` (healthy).

- [ ] **Step 2: Commit**

```bash
git add client/src/pages/DemoPage.tsx
git commit -m "refactor: update demo page colors to match new theme"
```

---

### Task 9: Create Layout Components

**Files:**
- Create: `client/src/components/layout/nav.tsx`
- Create: `client/src/components/layout/footer.tsx`
- Create: `client/src/components/layout/nav-loader.tsx`
- Create: `client/src/components/layout/root-layout.tsx`

- [ ] **Step 1: Create nav-loader.tsx**

```tsx
import { useState, useEffect, useRef } from "react";
import { useNavigation } from "react-router";

export function NavLoader() {
  const navigation = useNavigation();
  const isLoading = navigation.state === "loading";
  const [visible, setVisible] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout>>(null);

  if (isLoading && !visible) {
    setVisible(true);
  }

  useEffect(() => {
    if (!isLoading && visible) {
      timerRef.current = setTimeout(() => setVisible(false), 200);
      return () => {
        if (timerRef.current) clearTimeout(timerRef.current);
      };
    }
  }, [isLoading, visible]);

  return (
    <div className={`nav-loader ${visible ? "active" : ""}`}>
      <div className="nav-loader-bar" />
    </div>
  );
}
```

- [ ] **Step 2: Create nav.tsx**

```tsx
import { Link } from "react-router";
import { Radio, RotateCcw, Loader2 } from "lucide-react";
import { useChatStore } from "@/stores/chat";

export function Nav() {
  const { streaming, messages, clear } = useChatStore();
  const hasMessages = messages.length > 0;

  return (
    <nav className="border-b border-border/50 px-6 py-3 flex items-center justify-between backdrop-blur-sm sticky top-0 z-50 bg-surface/80">
      <div className="flex items-center gap-6">
        <Link to="/" className="flex items-center gap-2.5 group">
          <Radio className="h-4.5 w-4.5 text-primary" />
          <span className="font-heading text-sm font-bold tracking-wide text-foreground">
            fanout
          </span>
        </Link>
      </div>
      <div className="flex items-center gap-3">
        {streaming && (
          <div className="flex items-center gap-1.5 text-xs text-muted-foreground mono">
            <Loader2 className="h-3 w-3 animate-spin" />
            Streaming
          </div>
        )}
        {hasMessages && (
          <button
            onClick={clear}
            className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors px-2 py-1 rounded-md hover:bg-surface-2"
          >
            <RotateCcw className="h-3 w-3" />
            <span className="hidden sm:inline">New</span>
          </button>
        )}
      </div>
    </nav>
  );
}
```

- [ ] **Step 3: Create footer.tsx**

```tsx
export function Footer() {
  return (
    <footer className="w-full border-t border-border/30 mt-auto">
      <div className="max-w-[1200px] mx-auto px-4 sm:px-6 py-4">
        <div className="flex items-center justify-center">
          <span className="text-[11px] mono text-zinc-700">
            &copy; {new Date().getFullYear()} LabStack LLC
          </span>
        </div>
      </div>
    </footer>
  );
}
```

- [ ] **Step 4: Create root-layout.tsx**

```tsx
import { useRef, useEffect } from "react";
import { Outlet, useLocation } from "react-router";
import { Toaster } from "sonner";
import { NavLoader } from "./nav-loader";
import { Nav } from "./nav";
import { Footer } from "./footer";

const HIDE_FOOTER = new Set(["/"]);

export function RootLayout() {
  const { pathname } = useLocation();
  const mainRef = useRef<HTMLElement>(null);
  const showFooter = !HIDE_FOOTER.has(pathname);

  useEffect(() => {
    mainRef.current?.scrollTo(0, 0);
  }, [pathname]);

  return (
    <div className="h-screen flex flex-col noise">
      <NavLoader />
      <Nav />
      <main ref={mainRef} className="flex-1 min-h-0 overflow-y-auto flex flex-col">
        <div className="flex-1">
          <Outlet />
        </div>
        {showFooter && <Footer />}
      </main>
      <Toaster
        theme="dark"
        position="bottom-right"
        toastOptions={{
          style: {
            background: "var(--surface-2)",
            border: "1px solid var(--border)",
            color: "#d4d4d8",
            fontSize: "0.8125rem",
          },
        }}
      />
    </div>
  );
}
```

- [ ] **Step 5: Verify build compiles**

```bash
cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit
```

- [ ] **Step 6: Commit**

```bash
git add client/src/components/layout/
git commit -m "feat: add layout shell — Nav, Footer, NavLoader, RootLayout"
```

---

### Task 10: Wire Up Layout and QueryClient

**Files:**
- Modify: `client/src/main.tsx`
- Modify: `client/src/App.tsx`
- Modify: `client/src/pages/ChatPage.tsx`

- [ ] **Step 1: Update main.tsx to add QueryClientProvider**

```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import "./index.css";
import App from "./App";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 60_000,
      retry: (count, error) => {
        if (error && "status" in error && (error as { status: number }).status < 500) return false;
        return count < 1;
      },
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
```

- [ ] **Step 2: Update App.tsx to use RootLayout**

```tsx
import { BrowserRouter, Routes, Route } from "react-router";
import { RootLayout } from "./components/layout/root-layout";
import { ChatPage } from "./pages/ChatPage";
import { DemoPage } from "./pages/DemoPage";

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<RootLayout />}>
          <Route path="/demo" element={<DemoPage />} />
          <Route path="/*" element={<ChatPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

export default App;
```

- [ ] **Step 3: Update ChatPage.tsx — remove inline header (now in Nav)**

```tsx
import { useEffect } from "react";
import { useChatStore } from "@/stores/chat";
import { MessageList } from "@/components/chat/MessageList";
import { ChatInput } from "@/components/chat/ChatInput";
import { EmptyState } from "@/components/chat/EmptyState";

export function ChatPage() {
  const { init, messages } = useChatStore();

  useEffect(() => {
    const token =
      new URLSearchParams(location.search).get("token") ?? undefined;
    init(token);
  }, [init]);

  const hasMessages = messages.length > 0;

  return (
    <div className="flex flex-col h-full">
      <div className="flex-1 overflow-hidden">
        {hasMessages ? <MessageList /> : <EmptyState />}
      </div>
      <ChatInput />
    </div>
  );
}
```

Key changes:
- Removed header (now in `Nav` component)
- Changed `h-screen` to `h-full` (RootLayout manages the viewport)
- Removed imports for `Radio`, `RotateCcw`, `Loader2`, `streaming`, `clear` (now in Nav)

- [ ] **Step 4: Verify the app renders correctly in the browser**

Run: `cd /Users/v/Projects/labstack/fanout/client && npm run dev`

Check:
- Nav shows at top with fanout logo and streaming/new controls
- Chat page fills remaining space below nav
- Footer appears on `/demo` but not on `/` (chat)
- Toast notifications render (test: add `toast("test")` temporarily in a component)
- Body font is Fragment Mono
- Headings are DM Sans
- Blue accent everywhere instead of indigo/cyan

- [ ] **Step 5: Commit**

```bash
git add client/src/main.tsx client/src/App.tsx client/src/pages/ChatPage.tsx
git commit -m "feat: wire RootLayout with Nav/Footer, add QueryClientProvider, simplify ChatPage"
```

---

### Task 11: Create API Client

**Files:**
- Create: `client/src/api/client.ts`

- [ ] **Step 1: Create the fetch wrapper**

```typescript
export class ApiError extends Error {
  constructor(
    public status: number,
    public detail: string,
  ) {
    super(detail);
    this.name = "ApiError";
  }
}

let apiToken: string | null = null;

export function setApiToken(t: string | null) {
  apiToken = t;
}

export function getApiToken(): string | null {
  return apiToken;
}

async function fetchWithAuth(
  path: string,
  opts: RequestInit = {},
): Promise<Response> {
  const { headers: optsHeaders, ...rest } = opts;
  return fetch(path, {
    ...rest,
    headers: {
      "Content-Type": "application/json",
      ...(apiToken ? { Authorization: `Bearer ${apiToken}` } : {}),
      ...(optsHeaders as Record<string, string>),
    },
  });
}

export async function api<T>(
  path: string,
  opts: RequestInit = {},
): Promise<T> {
  const res = await fetchWithAuth(path, opts);

  if (!res.ok) {
    const body = await res.json().catch(() => ({ detail: res.statusText }));
    throw new ApiError(res.status, body.detail ?? res.statusText);
  }

  if (res.status === 204) return undefined as T;
  return res.json();
}
```

- [ ] **Step 2: Commit**

```bash
git add client/src/api/client.ts
git commit -m "feat: add API client with typed errors for React Query integration"
```

---

### Task 12: Final Visual QA

**Files:** None (manual verification)

- [ ] **Step 1: Start the dev server and verify all pages**

```bash
cd /Users/v/Projects/labstack/fanout && just up
```

- [ ] **Step 2: Check the chat page**

- Body font is Fragment Mono (monospace)
- Headings use DM Sans
- Background is `#09090b` (near-black)
- Nav is sticky with backdrop blur
- Logo accent is blue (#60a5fa)
- User message bubbles are blue (not indigo)
- Assistant avatar icon is blue
- Block cards have blue-tinted borders
- Tool status badges use green for completed
- Input glow animates in blue
- Scrollbar is thin and dark

- [ ] **Step 3: Check the demo page**

- Footer appears at bottom
- Charts use blue accent as primary series color
- Status colors: green/amber/red
- Block titles are blue uppercase mono

- [ ] **Step 4: Check noise overlay**

- Subtle grain texture visible across entire viewport

- [ ] **Step 5: Run the TypeScript build to ensure no type errors**

```bash
cd /Users/v/Projects/labstack/fanout/client && npx tsc --noEmit && npm run build
```

Expected: Clean build, no errors.

- [ ] **Step 6: Clean up the theme preview file**

```bash
rm /Users/v/Projects/labstack/fanout/client/theme-preview.html
```

- [ ] **Step 7: Final commit**

```bash
git add -A
git commit -m "chore: clean up theme preview file"
```

---

## Summary of Changes

| Area | Before | After |
|------|--------|-------|
| Theme | Light + dark (OKLCH) | Dark-only (hex, surface tiers) |
| Accent | Cyan/indigo `#818cf8` | Blue `#60a5fa` / `#3b82f6` |
| Body font | DM Sans (sans) | Fragment Mono (monospace) |
| Heading font | DM Sans | DM Sans (unchanged) |
| Layout | Chat-only, inline header | RootLayout (Nav + main + Footer + Toaster) |
| Data fetching | Raw fetch only | React Query (ready) + raw fetch (SSE) |
| Toasts | None | Sonner |
| Status colors | Hardcoded per-component | Centralized in `theme.ts` + CSS vars |
| Noise overlay | None | Subtle grain texture |
| Scrollbar | Dark-mode only | Global, styled to match surfaces |
| Animations | Basic | Monk-aligned (fadeUp, shimmer, dropdownIn, navLoad) |
| Custom utilities | None | `.mono`, `.stat-card`, `.detail-label`, `.btn-primary`, `.btn-ghost`, `.input-field`, `.shimmer` |
