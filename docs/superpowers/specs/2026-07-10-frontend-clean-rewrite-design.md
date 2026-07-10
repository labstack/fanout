# Frontend Clean Rewrite — Design & Standards

**Date:** 2026-07-10
**Status:** Draft for review
**Author:** design/eng session

## 1. Context & Decision

The current `web/` frontend (~10k LOC, ~2 months old, ported from *monk*) is
architecturally sound — route-splitting, TanStack Query with freshness
tracking, a documented token system, 17 D3/Recharts visualization blocks. A
diligence pass concluded a *reskin* would be the lower-risk path.

The owner has nonetheless chosen a **clean-room rewrite**: same tech stack,
keep the layout/IA, but write everything fresh from good patterns/standards
without porting the monk-derived code. This document records that decision and
the standards the rewrite is held to. The visual source of truth is the
approved "quiet instrument" Home mockup.

**Explicit choices made:**
- Build fresh in a parallel `web-next/` dir; swap to `web/` at parity.
- All 17 visualization blocks rebuilt from scratch, no reference to old code.
- Same stack: React 19 + Vite + Tailwind v4 + TanStack Query/Table + Zustand +
  Radix/CVA + zod. Icons move to **Phosphor** (`@phosphor-icons/react`).

## 2. Goals / Non-Goals

**Goals**
- A distinctive, coherent "quiet instrument" design language — calm by default,
  escalating to focused during incidents.
- Clean, strict, testable code with feature-first structure.
- Functional parity with today's four surfaces + supporting surfaces.
- IA aligned to the product vision: Home / Services / Investigate / Alerts.

**Non-Goals**
- No backend/API changes. The Go API contract is fixed; the frontend conforms.
- No new product features beyond what parity requires.
- No configurable dashboards, no RBAC (per product vision).

## 3. Design Language — "The Quiet Instrument"

Five pillars:

1. **State-driven density.** One system-pressure signal modulates the whole UI.
   Healthy = airy, near-colorless, one summary line. Incident = the page
   densifies and the broken thing earns a card + color. Fixed layout, dynamic
   emphasis.
2. **Space is the primary material.** 8pt rhythm, one column of attention,
   hairline dividers over boxes, flat surfaces (tint not shadow).
3. **Numbers are first-class typography.** Tabular mono numerals, aligned
   columns; charts support the number, not replace it.
4. **Color is severity, never decoration.** Mono chrome (inverted ink/paper);
   the only hues on the page are semantic status. A healthy board is nearly
   colorless.
5. **Motion settles, never bounces.** Movement only when meaning changes
   (incident rises in, resolved fades out). Respect `prefers-reduced-motion`.

**Two materials.** Deterministic surfaces (Home, Services) read flat and
confident — "this is math you can trust." The Investigate/AI surface gets a
distinct treatment so generated interpretation is never confused with computed
truth.

## 4. Design Tokens

Defined once in `styles/tokens.css` via Tailwind v4 `@theme`, consumed only
through utilities/variables — **no inline hex anywhere**. Both themes are
first-class; explicit `data-theme` overrides `prefers-color-scheme` in both
directions.

**Neutrals (cool-biased, picked):**
| token | light | dark (Ayu heritage) |
|---|---|---|
| `--paper` (ground) | `#f7f8f9` | `#0b0e14` |
| `--raised` | `#ffffff` | `#12151d` |
| `--raised-2` | `#f0f2f4` | `#1a1f2c` |
| `--line` | `#e4e7ea` | `#20242e` |
| `--ink` | `#14171d` | `#c5c8ce` |
| `--ink-2` (muted) | `#565b66` | `#7d8390` |
| `--ink-3` (faint) | `#878d99` | `#5c626e` |

**Chrome accent = contrast, not a hue:** `--accent` = ink, `--accent-fg` =
paper (inverted per dark). Vercel-mono heritage.

**Semantic status (calm, desaturated) — each with a soft `-bg` tint:**
`--ok` `#3f9d78` · `--warn` `#b7852b` · `--crit` `#cf574d` · `--info` `#4f7fb0`
(dark variants brightened). Status color is **separate from the accent** and
never used decoratively.

**Type.** Self-hosted (no CDN, CSP-safe): **Geist Sans** (UI) + **Geist Mono**
(all numerals/data), woff2 vendored in `styles/fonts/`. `tabular-nums` wherever
digits align. System stack is the fallback. Scale: 11/12.5/14/16.5/22/30, tight
tracking on large numerals, `.07em` uppercase micro-labels.

**Radius** `10px`. **Motion** durations 100/200/300ms, `cubic-bezier(.4,0,.2,1)`.

## 5. Information Architecture

Four primary surfaces in a persistent left rail; supporting surfaces footed.

| Route | Surface | Job |
|---|---|---|
| `/` | **Home** | Triage — do I act, and where? Adaptive density. |
| `/services` · `/service/:name` | **Services** | Deterministic RED view, endpoints, deps, errors. |
| `/investigate` | **Investigate** | AI chat workspace + rendered blocks. "Generated" material. |
| `/alerts` | **Alerts** | Firing / rules / history. Calm timeline that flares. |
| `/settings` `/login` `/demo` | supporting | Config, auth, public demo boot. |

View state (time window, namespace, filters, investigation) lives in the URL.

## 6. Engineering Standards (the "clean" contract)

- **TypeScript strict.** No `any`; no non-null `!` outside guarded scopes.
  `verbatimModuleSyntax`, `import type`. ESLint + typescript-eslint,
  `eslint-plugin-react-hooks`, no warnings in CI.
- **File naming: kebab-case.** One component per file, colocated with its
  hooks/styles. No barrel-file re-export sprawl (`index.ts` only where it earns
  its keep).
- **Feature-first structure**, not type-first. A surface owns its components,
  queries, and types.
- **Data layer.** One typed `fetch` wrapper → **zod-validated** responses →
  **TanStack Query** hooks per resource. No `fetch`/`useQuery` inline in view
  components. Errors are typed (`ApiError`).
- **State discipline.** URL owns view state. Zustand only for genuinely
  cross-cutting client state (chat session, auth, theme). Server state lives in
  Query and is never mirrored into Zustand.
- **Styling.** Tailwind v4 `@theme` tokens + **CVA** for variants +
  `tailwind-merge`. No inline hex; tokens only. `cn()` helper for class merge.
- **Components.** Radix primitives for behavior; thin CVA skins for appearance.
  Accessible names, focus-visible states, keyboard paths — acceptance criteria,
  not afterthoughts.
- **Testing.** Vitest + Testing Library for logic and component behavior;
  scoring/ranking/formatting logic unit-tested. Charts get render smoke tests.
- **Performance.** Route-level `lazy()` splitting; virtualize long lists
  (`@tanstack/react-virtual`); `uPlot` for dense/streaming timeseries, Recharts
  for report blocks, D3 for bespoke geometry.

## 7. Target Structure

```
web-next/src/
  app/              # entry, router, providers (query, auth, theme)
  shared/
    ui/             # button, card, dialog, tabs… (Radix + CVA)
    lib/            # api client, zod schemas, formatters, cn, hooks
    charts/         # sparkline, timeseries (uPlot), chart theme
  features/
    home/           # triage: page, incident-card, service-list, queries
    services/       # list + detail: red-metrics, endpoints, deps, errors
    investigate/    # chat workspace, block-renderer, 17 blocks
    alerts/         # firing, rules, create-rule, history
    auth/  settings/  demo/
  styles/           # tokens.css, globals.css, fonts/
```

## 8. Visualization Blocks (from scratch)

All 17 rebuilt against a clean `Block` interface and the new token/chart theme.
Grouped by build cost:

- **Trivial:** text, metric, badge, bar, table (TanStack Table), logs.
- **Charting (Recharts/uPlot):** timeseries, comparison, correlation, heatmap,
  metrics, endpoints.
- **Hard / bespoke D3 (highest risk):** flame-graph, trace-waterfall,
  topology (d3-force), sankey, dep-matrix. These are the schedule risk; each
  gets its own plan step and a golden-render test.

A shared `block-renderer` maps block-type → component; `block-actions` handles
drill-down navigation.

## 9. Build Order (vertical slices, each shippable)

1. **Foundation** — `web-next/` scaffold: Vite + Tailwind v4 + TS strict +
   ESLint + Vitest; `tokens.css` + fonts; app shell (rail + router + providers);
   api client + zod + Query setup.
2. **Home** — proves the whole pattern end-to-end against `/api/overview`.
3. **Services** — list + detail (RED, endpoints, deps, errors).
4. **Alerts** — firing / rules / create-rule / history.
5. **Investigate + blocks** — chat workspace, block-renderer, then blocks by
   cost tier (trivial → charting → bespoke D3).
6. **Auth / settings / demo** — login, API-key/settings, public-read boot.
7. **Cutover** — parity checklist, then `web-next/` → `web/`.

## 10. Open Items

- **API contract** is derived from the Go backend (`internal/api`,
  `internal/service`) — the source of truth — not the old frontend. First plan
  step documents each endpoint + response shape as zod schemas.
- **Fonts:** confirm Geist Sans/Mono self-host vs. system stack.
- **uPlot vs Recharts split** validated during the Home/Services slices.
