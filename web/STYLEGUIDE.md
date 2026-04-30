# Web Style Guide

The single source of truth for visual decisions in `web/`. If a rule isn't here, it isn't a rule — propose it as a PR.

When something feels off, the answer is "follow this guide", not "negotiate per file." Drift is a bug.

---

## Philosophy

Fanout's web is an **observability dashboard** — dense, scannable, dark-first, deterministic over decorative. Operators glance at it during incidents; visual restraint is what makes the signal cut through. If a token, size, or component fights the aesthetic, it's wrong even if it works.

Three rules above all others:
1. **Use semantic tokens, not hex.** `text-danger`, never `text-[#f43f5e]`.
2. **Use the scale, not arbitrary values.** `gap-2`, never `gap-[7px]`.
3. **Use existing primitives, not new markup.** Check `components/ui/` and `components/{domain}/` first.

---

## Color

### Tokens

All colors live in `src/app.css` as CSS custom properties and as Tailwind utilities. **Never write a hex literal in JSX.**

| Token | Hex | Tailwind | Use |
|---|---|---|---|
| `--background` | `#09090b` | `bg-background` | Page background |
| `--card` | `#121215` | `bg-card` | Cards, panels, sidebars |
| `--surface-2` | `#1a1a1f` | `bg-surface-2` | Hover targets, secondary surfaces |
| `--surface-3` | `#252529` | `bg-surface-3` | Track backgrounds (meters, scrollbars) |
| `--border` | `#2a2a30` | `border-border` / `border-input` | All borders |
| `--primary` | `#60a5fa` | `bg-primary` / `text-primary` / `ring-ring` | Brand interactive (active tab, focus ring, primary button). Never as a track or fill background. |
| `--success` | `#34d399` | `text-success` / `bg-success` | Healthy, OK, recovered |
| `--danger` | `#f87171` | `text-danger` / `bg-danger` | Errors, unhealthy, firing alerts |
| `--warning` | `#fbbf24` | `text-warning` / `bg-warning` | Degraded, pending, amber states |
| `--info` | `#a78bfa` | `text-info` / `bg-info` | Neutral notice, informational |
| `--healthy` | `#34d399` | `text-healthy` / `bg-healthy` | Service status — alias of `success` |
| `--degraded` | `#fbbf24` | `text-degraded` / `bg-degraded` | Service status — alias of `warning` |
| `--unhealthy` | `#f87171` | `text-unhealthy` / `bg-unhealthy` | Service status — alias of `danger` |
| `--foreground` | `#e4e4e7` | `text-foreground` | Primary text |
| `--muted-foreground` | `#71717a` | `text-muted-foreground` | Secondary text, labels, captions |

### Banned in JSX

```tsx
// Never
className="text-[#34d399]"
className="border-[#a78bfa]/60"
style={{ color: "#71717a" }}

// Always
className="text-success"
className="border-info/60"
className="text-muted-foreground"
```

If you find an existing hex, replace it in the same PR you're touching that file.

### Semantic rules

- **Color must not be the only signal.** Pair `text-success` / `text-danger` with `↑` / `↓` glyphs or text labels for accessibility.
- **Primary is for interactive selection.** Active tab, focused input ring, primary button. Not for backgrounds, decoration, or meter fills.
- **`text-warning` is a third state**, not a "decoration in between." Use it to mean *degraded / pending / amber*, not "I want a yellow color here."
- **Use opacity modifiers, not new tokens.** `text-foreground/60`, `border-border/40`. Don't define a new color when an existing one with reduced opacity works.
- **Service-status aliases (`healthy`/`degraded`/`unhealthy`)** map to the canonical `success`/`warning`/`danger` and exist for readability when the variable *is* a service health value. Use canonical names everywhere else.

---

## Typography

### Fonts

| Font | Variable | Use |
|---|---|---|
| **JetBrains Mono** | `--font-mono` | Numeric data, badges, labels, dates, code, anything tabular |
| **Fragment Mono** | `--font-sans` (loaded as the body sans default) | Body text, summaries, narrative prose |
| **DM Sans** | `--font-heading` | Page titles, card titles (`h1`–`h6` by default) |

**Why two monospaces?** Fragment Mono is friendlier for prose; JetBrains Mono has tighter rhythm for numbers. The body looks "terminal" without sacrificing readability.

Use `font-mono` to opt into JetBrains Mono explicitly. Never set `font-family` inline.

### Size scale

The codebase uses 5 working sizes plus a few display sizes for hero stats. Stick to these:

| Class | Pixel | Use |
|---|---|---|
| `text-[10px]` | 10 | Sparse table captions; mono badge text |
| `text-[11px]` | 11 | **Eyebrow labels** (uppercase metadata) |
| `text-xs` (12) | 12 | Meta captions, dense rows |
| `text-[13px]` | 13 | Card body, log rows |
| `text-sm` (14) | 14 | **Body default** |
| `text-base` (16) | 16 | Prose, summary paragraphs |
| `text-lg` (18) | 18 | Sub-heading, mobile card title |
| `text-2xl` (24) | 24 | Section heading |
| `text-[30px]+` | 30+ | Hero stat numbers |

**Banned**: `text-[8px]`, `text-[9px]` (sub-10px breaks accessibility), and any `text-[Npx]` outside this list.

### Letter-spacing

| Class | Use |
|---|---|
| (none) | Default — body, titles, regular-case prose |
| `tracking-wide` | Pill labels, badges (light emphasis) |
| `tracking-[0.16em]` | **Uppercase eyebrow labels** (the canonical mono uppercase look) |

### Casing

- `uppercase` is reserved for eyebrow labels and badges (mono + tracking-[0.16em]).
- Titles in DM Sans stay in normal case.
- Numbers are mono and natural.

---

## Spacing

Use the **4-grid** (Tailwind default). Approved values:

| Class | Pixel |
|---|---|
| `*-0.5` | 2 |
| `*-1` | 4 |
| `*-1.5` | 6 |
| `*-2` | 8 |
| `*-2.5` | 10 |
| `*-3` | 12 |
| `*-4` | 16 |
| `*-5` | 20 |
| `*-6` | 24 |
| `*-8` | 32 |
| `*-10` | 40 |
| `*-12` | 48 |

**Banned**: `gap-[3px]`, `gap-[5px]`, `gap-[7px]`, `gap-[10px]`, `mb-[10px]`, `py-[5px]`, etc. Map to the nearest scale value.

### Density rules

- **Card padding**: `p-4` (mobile) / `p-5` or `p-6` (desktop).
- **Section gap inside a card**: `gap-3` or `space-y-3`. Loose: `gap-4`.
- **Page sections**: `space-y-6` or `space-y-8` between major blocks.
- **Container padding (page)**: `px-4 sm:px-6` always.

---

## Border radius

| Class | Pixel | Use |
|---|---|---|
| `rounded-sm` | 3 | Meter bars, dense indicators |
| `rounded-md` | 8 | Buttons, dropdowns, inputs |
| `rounded-lg` | 12 | Cards, panels, table cells |
| `rounded-xl` | 16 | Large cards |
| `rounded-2xl` | 20 | Hero card |
| `rounded-full` | — | Pills, chips, badges |

---

## Borders

- `border` (1px) is the default. Don't use `border-2` for cosmetic emphasis — use color instead.
- `border-border` is the only border color. Use `/40`, `/60` opacity for subtler dividers.
- `border-primary` only for interactive-selected state.

---

## Motion

| Token | Use |
|---|---|
| `duration-100` | Quick micro-interactions (hover color, focus ring) |
| `duration-200` | Default transitions (open/close menus, swap state) |
| `duration-300` | Page-level transitions, large surface changes |
| `transition-colors` | **The default — covers 95% of cases** |
| `transition-opacity` | Show/hide animations |
| `transition-transform` | Move/scale animations |

**Banned**: `transition-all` — too broad, animates layout properties causing jank. Pick the specific transition.

---

## Components

### Where to put a new component

```
components/
├── ui/                  # shadcn primitives. Pristine, app-independent.
│                        # Add via shadcn CLI; do not hand-write here.
├── states/              # loading/error/empty primitives
├── layout/              # app-shell, page-container, page-header, nav, footer
└── {domain}/            # Domain composites: home/, service/, alerts/, chat/, blocks/
                         # Built from ui/ primitives.
```

### Rules

- **Always check `components/ui/` first.** If a primitive exists, use it.
- **Domain components live in `components/{domain}/`.** A `firing-alerts` belongs in `components/alerts/`, not `components/ui/`.
- **Pages live in `pages/` and orchestrate domain components.** Pages don't render raw markup beyond layout.
- **Naming**: file names kebab-case (`firing-alerts.tsx`); exports PascalCase (`export function FiringAlerts`).

### Props

- Components that participate in layout **must accept `className`** and merge it via `cn()`.
- Variant-driven components use `cva` (`class-variance-authority`).
- Mutually exclusive props should be a discriminated union, not "this is ignored when that is set."

---

## Iconography

- **`lucide-react`** is the only icon set. No emoji as icons. No custom SVG unless it's a chart, illustration, or brand mark.
- Icons inside text get `inline-block size-4` (16px) by default, smaller in dense rows (`size-3` / `size-3.5`).
- Decorative icons get `aria-hidden="true"`. Functional icons (close button, etc.) get `aria-label`.

---

## Accessibility

These are non-negotiable. PRs that regress these get rejected.

### Tap targets

- Minimum **44×44px** on mobile for any clickable element.
- Desktop targets can shrink, but be deliberate.

### Color contrast

- Body text: **≥ 4.5:1** against the surface it sits on (WCAG AA).
- Labels and captions: **≥ 3:1** (WCAG AA Large).
- Avoid `text-foreground/40` for anything readable; reserve for placeholders or disabled.

### Focus

- **Every interactive element has a visible focus ring.** `focus-visible:ring-2 focus-visible:ring-ring/40` is the canonical.
- Don't `outline-none` without replacing it.

### Color is not the only signal

- Health values get text labels (`HEALTHY`, `DEGRADED`, `UNHEALTHY`) — not just colored dots.
- Required form fields have `*` or `required` text — not just red borders.

### Charts

- Every chart has an `aria-label` summarizing range and key values.
- Tooltips reachable by keyboard.

---

## Layout

### Page shell

Every page must use the standard shell:

```tsx
<PageContainer>
  <PageHeader title="..." subtitle="..." />
  {/* content */}
</PageContainer>
```

`<PageContainer>` provides `max-w-[1400px] mx-auto px-4 sm:px-6 pt-6 pb-20 fade-up`.

### Mobile layout switches

When the desktop layout doesn't fit at `< md` (768px):

1. **Tables → cards.** Build a `md:hidden` card list and `hidden md:block` table side-by-side.
2. **Multi-column → stacked.** `grid-cols-1 md:grid-cols-2`.

Don't ship a layout where mobile users have to horizontal-scroll.

### Loading / Empty / Error states

Three canonical primitives in `components/states/`:

- **`<LoadingState />`** — skeleton/spinner matching the actual layout.
- **`<EmptyState title="..." />`** — muted mono caption, optional CTA. No illustrations.
- **`<ErrorState error={…} resetErrorBoundary={…} />`** — `text-danger` mono caption with a one-line action.

Use them; don't hand-roll per page.

---

## Imports

- Always absolute: `@/components/...`, `@/api/...`, `@/lib/...`.
- Never relative beyond one level (`../`).
- `import type { … }` for type-only imports (`@typescript-eslint/consistent-type-imports`).

---

## Naming

- Files: kebab-case (`firing-alerts.tsx`, `use-media-query.ts`).
- Components / types: PascalCase.
- Variables / functions / hooks: camelCase. Hooks start with `use`.
- Constants: SCREAMING_SNAKE_CASE for module-scope; PascalCase or camelCase for in-function.
- Booleans: `isX`, `hasX`, `canX`, `shouldX`.

---

## Comments

Default to no comments. Only add one when:

- A non-obvious **why** would surprise a reader.
- A workaround exists for a known bug or browser quirk.
- A subtle invariant matters and isn't visible from types.

Don't write comments that explain **what** the code does — well-named identifiers do that.

Don't reference the current task, fix, or callers. PR descriptions live in PRs, not in the source.

---

## What's enforced automatically

The styleguide is partially enforced by lint (see `eslint.config.js`):

- Hex literals in `className` strings — error
- `cursor-pointer` on `<button>` / `<a>` — error
- `transition-all` — error
- `consistent-type-imports`, `switch-exhaustiveness-check`, `no-non-null-assertion`, `no-explicit-any` — error

The rest is enforced by **code review**. Cite this document by section.

---

## Updating this guide

1. Make the rule change in this document via PR.
2. Add or update lint rules where possible.
3. Refactor existing usage to match in the **same PR** if scope permits, otherwise open a follow-up labeled `style-debt`.

Don't just write a rule and call it done — leaving violations in the codebase undermines every future enforcement.
