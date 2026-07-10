# Frontend Clean Rewrite — Design & Standards

**Date:** 2026-07-10
**Status:** Reviewed (3 independent reviews folded in) — ready for planning
**Author:** design/eng session

## 1. Context & Decision

The current `web/` frontend (~10k LOC, ~2 months old, ported from *monk*) is
architecturally sound. A diligence pass concluded a *reskin* would be
lower-risk; the owner nonetheless chose a **clean-room rewrite** — same stack,
keep the layout/IA, write everything fresh from good patterns without porting
the monk-derived code. This document records that decision and the standards
the rewrite is held to. The visual source of truth is the approved "quiet
instrument" Home mockup.

**Explicit choices (owner-confirmed):**
- Build fresh in a parallel `web-next/` dir; swap to `web/` at parity.
- All 17 visualization blocks rebuilt from scratch, no reference to old code.
- **`block-actions` drill-down ranking and the bespoke D3 layout geometry are
  also rebuilt from scratch** (no reference). Reviews flagged this as the
  highest regression risk; accepted, mitigated by heavy behavior + interaction
  tests written from the vision docs (§8).
- **API contract via codegen**: extend the existing `cmd/genblocks` Go
  generator to emit zod schemas + inferred TS types. "Don't look at existing
  code" applies to the monk-era TSX, **not** to the generator or the Go structs
  that are the contract's source of truth.
- Same stack: React 19 + Vite + Tailwind v4 + TanStack Query/Table + Zustand +
  Radix/CVA + zod + **react-hook-form**. Icons move to **Phosphor**
  (`@phosphor-icons/react`, owner preference).

## 2. Goals / Non-Goals

**Goals**
- A distinctive, coherent "quiet instrument" design language — calm by default,
  escalating to focused during incidents, across the full incident lifecycle.
- Clean, strict, testable code; feature-first structure.
- Functional parity with today's surfaces (§9 parity checklist is the gate).
- IA aligned to product vision: Home / Services / Investigate / Alerts.

**Non-Goals**
- No backend/API changes (the Go binary embeds and serves the SPA; the frontend
  conforms to the existing contract).
- No new product features beyond parity.
- No configurable dashboards, no RBAC (per product vision).

## 3. Design Language — "The Quiet Instrument"

Six pillars:

1. **State-driven density.** One system-pressure signal modulates the whole UI.
   Healthy = airy, near-colorless. Incident = the page densifies and the broken
   thing earns a card + color. Fixed layout, dynamic emphasis.
2. **Space is the primary material.** 8pt rhythm, one column of attention,
   hairline dividers over boxes, flat surfaces (tint not shadow).
3. **Numbers are first-class typography.** Tabular mono numerals, aligned
   columns; charts support the number, not replace it.
4. **Color is severity, never decoration** — *but never color alone.* Every
   status ships **color + shape + label** together (e.g. ● healthy / ◐ degraded
   / ▲ unhealthy, with a status word), so the red/green axis is legible to
   colorblind users and to a suppressed-color calm board. Chart series pair hue
   with dash/label/annotation. This redundancy is a hard rule, not a nicety.
5. **Motion settles, never bounces.** Movement only when meaning changes
   (incident rises in, resolved fades). Respect `prefers-reduced-motion`.
6. **Calm is confirmed, not empty.** A healthy screen carries an explicit
   positive affirmation + freshness ("12 services healthy · updated 4s ago") so
   "all clear" never reads the same as "pipeline is dead / no data."

**Full incident lifecycle.** The design language must cover the whole arc the
vision defines, not just two endpoints. Each state maps to a density + color
treatment:

| State | Treatment |
|---|---|
| **Empty / no-data** (first run) | Onboarding affirmation, "waiting for OTLP", no alarm color |
| **Healthy** | Airy, near-colorless, affirmation + freshness (pillar 6) |
| **Degraded** ("watch") | Warm accent, single watch card, page stays calm |
| **Incident** | Crit accent, incident card rises in, affected services sort up + colorize |
| **Recovery** (v2) | "was wrong, now healing" card, cooling treatment |
| **Cascade** (v2) | Grouped related incidents |

Recovery + Cascade are v2 per the vision; the language reserves treatment for
them but v1 ships Empty/Healthy/Degraded/Incident.

**Two materials (concrete, not hand-wavy).** Deterministic surfaces (Home,
Services, Alerts) render flat, mono, confident — computed truth. The
Investigate/AI surface is visually distinct so generated interpretation is never
mistaken for computed fact: **generated surfaces get a faintly tinted ground
(`--gen-bg`), an "AI" chrome label, and non-mono body type**; computed surfaces
stay flat/mono. Pinned to tokens so it is testable, not a re-decision at build
time.

## 4. Design Tokens

Defined once in `styles/tokens.css` via Tailwind v4 `@theme`; consumed only
through utilities/variables — **no inline hex** (ESLint bans hex in
`className`; uPlot colors go through a CSS-var→canvas bridge, the one lint
exception). Both themes first-class; explicit `data-theme` overrides
`prefers-color-scheme` in both directions.

**Neutrals (cool-biased, picked):**
| token | light | dark (Ayu heritage) | notes |
|---|---|---|---|
| `--paper` | `#f7f8f9` | `#0b0e14` | ground |
| `--raised` | `#ffffff` | `#12151d` | card/row |
| `--raised-2` | `#f0f2f4` | `#1a1f2c` | inset/hover |
| `--line` | `#e4e7ea` | `#20242e` | hairline |
| `--ink` | `#14171d` | `#c5c8ce` | primary text ✓ |
| `--ink-2` | `#565b66` | `#7d8390` | **all muted labels** (passes 4.5:1) |
| `--ink-3` | `#878d99` | `#5c626e` | **hairlines/dividers only — never text** |

> Review fix: `--ink-3` failed contrast (3.13/3.15, and 2.69 on dark cards).
> It is reclassified as a non-text token. Every label uses `--ink-2`.

**Chrome accent = contrast, not a hue:** `--accent` = ink, `--accent-fg` =
paper (inverted per theme). Vercel-mono heritage.

**Semantic status — two ramps.** A *calm mark* ramp (dots, fills, tints) and a
darker *text* ramp that clears WCAG 4.5:1 on light ground:

| | calm mark (light) | dark theme | **text-on-light (≥4.5:1)** |
|---|---|---|---|
| `--ok` | `#3f9d78` | `#4bb489` | `--ok-text` ~`#2b6f52` |
| `--warn` | `#b7852b` | `#cf9d42` | `--warn-text` ~`#8a6410` |
| `--crit` | `#cf574d` | `#e0685d` | `--crit-text` ~`#b8322a` |
| `--info` | `#4f7fb0` | `#6b9bd0` | `--info-text` ~`#3a6a95` |

> Review fix: light-mode status colors failed 4.5:1 as text (ok 3.13 / warn
> 3.08 / crit 3.85 / info 3.95). Rule: **colored numbers/text use the `-text`
> ramp; the number itself prefers `--ink` with a status mark beside it.** Dark
> ramp already passes. Exact `-text` hexes tuned + asserted in the Foundation
> slice; each `-bg` tint must clear 4.5:1 for any text placed on it.

**Type.** Self-hosted (no CDN, CSP-safe): **Geist Sans** (UI) + **Geist Mono**
(numerals/data), woff2 subset vendored in `styles/fonts/`, `@font-face` +
preload; system stack is the runtime fallback. `tabular-nums` wherever digits
align. Scale 11/12.5/14/16.5/22/30; tight tracking on large numerals; `.07em`
uppercase micro-labels (in `--ink-2`).

**Radius** `10px`. **Motion** 100/200/300ms, `cubic-bezier(.4,0,.2,1)`.

## 5. Information Architecture

Four primary surfaces in a persistent left rail; supporting surfaces footed.

| Route | Surface | Job |
|---|---|---|
| `/` | **Home** | Triage across the full lifecycle (§3). Adaptive density. |
| `/services` · `/service/:name` | **Services** | Deterministic RED, endpoints, deps, errors. |
| `/investigate` | **Investigate** | SSE AI chat + rendered blocks. "Generated" material. |
| `/alerts` | **Alerts** | Firing / rules / create-rule / history. Calm timeline that flares. |
| `/login` `/settings` | supporting | 3-step setup/email/code auth; settings + API-key admin. |
| `/demo` | fixture gallery | Static block gallery — the dev harness for building the 17 blocks. |

**Public-read mode** is a cross-cutting auth state, *not* a route: when the
server reports `public_read`, an anonymous read-only viewer is synthesized and
the app boots login-less (distinct from `/demo`).

**View state lives in the URL** (time window, namespace, filters, investigation).

**Supporting-surface scope decisions:**
- **Bookmarks** (`/api/bookmarks`) and **Suggestions** (`/api/suggestions`) —
  scope call in the plan; default *keep* (small).
- **Reports** (`/reports`, `/view/r/:id`) — server-rendered, likely **not** a
  React surface; confirm and mark **out of scope** for the SPA rewrite.

## 6. Engineering Standards (the "clean" contract)

- **TypeScript strict + `noUncheckedIndexedAccess`** (array/chart-heavy app;
  cheap greenfield, painful retrofit). No `any`; no non-null `!` outside guarded
  scopes. `verbatimModuleSyntax`, `import type`. ESLint + typescript-eslint +
  `eslint-plugin-react-hooks` + **`eslint-plugin-jsx-a11y`**, zero warnings in
  CI. Carry forward the existing hex-in-className ban and `transition-all` ban.
- **File naming: kebab-case.** One component per file, colocated. No barrel-file
  re-export sprawl.
- **Feature-first structure** (§7). A surface owns its components, queries, and
  *view* types.
- **Data layer.** One typed `fetch` wrapper with the **refresh interceptor**
  (in-memory Bearer + httpOnly refresh cookie + auto-refresh-on-401 with promise
  dedupe + "keep token on 5xx"). Responses **zod `safeParse`**; on failure
  **log + degrade, never throw** (hot 30s-poll paths). No `fetch`/`useQuery`
  inline in view components. Errors typed (`ApiError`).
- **Query-key discipline.** The **token is never in the query key** (auth change
  invalidates queries instead). **Namespace is threaded via a shared
  `useNamespace()` + a query-key factory** so no resource can silently omit it.
- **State discipline.** URL owns view state. Zustand only for cross-cutting
  client state (chat session, auth, theme). Server state lives in Query, never
  mirrored into Zustand.
- **Forms.** react-hook-form + `zodResolver` + a shared `ui/form.tsx`. Auth
  (3-step) and create-rule use it; no ad-hoc `useState` forms.
- **Chat transport is SSE** (`POST /api/chat`, `/api/chat/cancel`, `/clear`) via
  a single `ChatClient`; both the chat surface and alert rule-assist consume the
  one client (today they parse SSE in two places).
- **Styling.** Tailwind v4 `@theme` tokens + CVA variants + `tailwind-merge`
  (`cn()`); tokens only.
- **Components.** Radix for behavior; thin CVA skins for appearance. Accessible
  names, focus-visible, keyboard paths. Shared **loading / empty / error**
  primitives are required on every surface.
- **Block safety.** `block-renderer` uses `lazy()` for charting + bespoke-D3
  tiers (so Investigate doesn't pull every viz lib at once) and wraps **each
  block in its own error boundary** (block `data` is untrusted/AI-generated).
- **Testing + CI.** Add **vitest + @testing-library + jest-axe** (net-new; no
  test infra exists today). Unit-test scoring/ranking/formatting;
  component-test behavior; **interaction tests** (not just golden-render) for
  D3 blocks; axe smoke per surface. Gate: `lint + tsc -b + test` wired into
  `just check`. **Bundle-size budget** in CI (3 chart libs + D3 + fonts).
- **Performance.** Route-level `lazy()`; virtualize long lists
  (`@tanstack/react-virtual`); `uPlot` for dense/streaming timeseries (validated
  vs Recharts in Home/Services first), Recharts for report blocks, D3 for
  bespoke geometry.

## 7. Target Structure

```
web-next/src/
  app/              # entry, router, providers (query, auth, theme)
  shared/
    ui/             # button, card, dialog, tabs, form… (Radix + CVA)
    lib/            # api client, GENERATED zod schemas + types, formatters, cn, hooks
    charts/         # sparkline, timeseries (uPlot), chart theme, CSS-var bridge
  features/
    home/           # triage: page, incident-card, service-list, state machine, queries
    services/       # list + detail: red-metrics, endpoints, deps, errors
    investigate/    # chat client + store, block-renderer, 17 blocks, block-actions
    alerts/         # firing, rules, create-rule (RHF), history
    auth/  settings/  demo/
  styles/           # tokens.css, globals.css, fonts/
```

**Type ownership rule:** generated API zod schemas + inferred types live in
`shared/lib` (consumed by both shared chrome — e.g. the nav health badge reads
`overview` — and features). Feature-local *view* types stay in the feature.

## 8. Visualization Blocks (from scratch)

All 17 rebuilt against a clean `Block` interface + the new token/chart theme;
`block-actions` drill-down ranking rebuilt from scratch too (owner decision).
No reference to old code — so correctness is pinned by **tests authored from the
vision docs**, not by diffing old output. Grouped by build cost:

- **Trivial:** text, metric, badge, bar, table (TanStack Table), logs.
- **Charting (Recharts/uPlot):** timeseries, comparison, correlation, heatmap,
  metrics, endpoints.
- **Bespoke D3 (highest risk, each its own plan step + interaction tests):**
  trace-waterfall (~323), topology/d3-force (~264), flame-graph (~205),
  dep-matrix (~154), sankey (~112). Golden-render proves a static frame;
  **zoom/hover/force-sim interaction is ~80% of the work and needs its own
  tests.**

The `/demo` fixture gallery is built **before** the blocks and is the harness
used to develop them.

## 9. Build Order & Cutover

Vertical slices, each shippable. Contract + cutover wiring come first.

0. **Contract + harness.** Extend `cmd/genblocks` to emit zod + TS types into
   `shared/lib`. Scaffold `web-next/` and **wire it into `just build` + the Go
   `//go:embed` path behind a flag now**, so the served-binary/TLS/routing path
   is exercised continuously, not discovered at swap. Draft the parity
   checklist (below).
1. **Foundation.** Vite + Tailwind v4 + TS strict (+`noUncheckedIndexedAccess`)
   + ESLint(+a11y) + vitest/axe + CI gate; `tokens.css` (tune + assert the
   `-text` ramps) + fonts; app shell (rail + router + providers); api client
   (refresh interceptor) + Query + forms + states primitives.
2. **Home** — full lifecycle (empty/healthy/degraded/incident); proves the
   pattern against `/api/overview`.
3. **Services** — list + detail (RED, endpoints, deps, errors).
4. **Alerts** — firing / rules / history + **create-rule (RHF + expr-lang zod
   schema; its own sized step, ~470 LOC today)**.
5. **Demo fixture gallery** — harness for the blocks.
6. **Investigate (split ≥5):** (a) SSE `ChatClient` + chat store + shell,
   (b) block-renderer + error boundaries + trivial blocks, (c) charting blocks,
   (d) each bespoke-D3 block, (e) `block-actions` drill-down.
7. **Auth / settings / public-read** — 3-step setup/email/code, settings +
   API-key admin, anonymous read-only boot.
8. **Supporting surfaces** — bookmarks / suggestions per §5 scope calls.
9. **Cutover** — parity checklist green → `web-next/` becomes `web/`; the flag
   from step 0 is the rollback.

**Parity checklist (gate for cutover)** — derived from current behavior:
public-read anon boot · first-run setup + one-time ingest-token reveal ·
`?token=` embed path · namespace switching threads every query · per-resource
refetch cadences (freshness) · loading/empty/error on every surface · all 17
blocks render + interact · SSE chat stream + cancel + clear · demo gallery ·
same-origin API contract.

## 10. Open Items

- **Fonts:** confirm self-hosted Geist Sans/Mono vs. system stack (§4 assumes
  Geist).
- **uPlot vs Recharts split** validated during Home/Services.
- **Bookmarks/Suggestions** keep-or-drop; **Reports** confirmed out-of-scope.
- **`?token=` embed / credential-in-URL**: make a deliberate security decision
  during the auth slice (keep for embed vs. drop), don't inherit silently.
- **`lucide-react@1.8.0`** major looked unusual to review — verify before the
  Phosphor swap; decide the fate of the existing emoji-icon map
  (`emoji-icons.tsx` + `emoji-regex`).
