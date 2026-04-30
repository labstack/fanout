# Web patterns

Conventions for `web/`. Most are also enforced by `eslint.config.js`; this file is the human reference and the place to look when a lint rule fires.

## Variant vocabulary

Components never see domain words. The canonical set is identical across `Badge`, `Button`, `Alert`, `Toast`, `StatusBadge`:

```
default | secondary | success | danger | warning | info | neutral | outline | ghost | link
```

**Banned variant names**:

- `healthy`, `degraded`, `unhealthy` (these are color tokens, not variant names — use `success`/`warning`/`danger`)
- `firing`, `recovered` (use `danger`/`success`)
- `positive`, `negative` (use `success` / `danger`)
- `error` (use `danger`)
- `destructive` (use `danger`)

**Domain mapping lives in `lib/*-variants.ts`.** Helpers like `serviceStatusVariant("unhealthy") → "danger"` translate domain words to the canonical vocabulary. Helpers MAY ONLY return values from the canonical set — never invent new variants for domain reasons.

## Token slots

Color values live in `app.css` token slots. Component code uses Tailwind utilities that resolve through the slots (`bg-card`, `text-success`, `border-warning/60`).

**Banned in `className`** (lint-checked):

- Hex literals: `text-[#34d399]`, `bg-[#f87171]/10`, `border-[#fbbf24]` — use `text-success`, `bg-danger/10`, `border-warning`
- `transition-all` — use specific transitions (`transition-colors`, `transition-opacity`, `transition-transform`, `transition-shadow`); `transition-all` causes layout jank
- `cursor-pointer` on `<button>` or `<a>` — both already have `cursor: pointer`

## State surfaces

**Every page renders all three of:**

1. **Loading** — `<LoadingState />` while a primary query is in flight
2. **Error** — `<ErrorState error={…} resetErrorBoundary={…} />` when a query fails
3. **Empty** — `<EmptyState title="…" />` when the query succeeded but returned zero results

These are in `web/src/components/states/`. Use them; don't hand-roll per page.

## Color is never the only signal

Pair color with a glyph or sign so colorblind users can read the meaning:

- `text-success` paired with `↑` (gain) or text label
- `text-danger` paired with `↓` (loss) or text label
- `text-warning` paired with `⚠` or contextual icon

Same rule for status indicators: don't rely on a green dot alone — pair with text.

## Data fetching

- **TanStack Query for server state.** No `useEffect + fetch`.
- **One primary fetch per page.** If a page needs N queries, compose hooks (`useServiceDetail` → calls `useService` + `useEndpoints` internally). Pages should look like data is one thing.
- **Query keys start with the resource:** `["overview"]`, `["service", name]`, `["alerts", { state }]`. Mutations invalidate by resource.
- **Dependent queries use `enabled: Boolean(parent?.id)`** — never branch on conditional render.
- **`<RequireAuth>` for protected routes.** Today it wraps every authenticated page in `App.tsx`.

## API errors

- Server errors come back as `ApiError` with `{status, message}` (see `api/client.ts`).
- For form-level errors, surface via `<FormMessage>` (RHF). Field-level mapping comes when the backend adds `fields`.
- For page-level errors, throw to the boundary; `<ErrorState>` extracts the message.
- `setApiToken()` is the hook for swapping the auth token; don't reach into `client.ts` for token state directly.

## Auth hooks

- **`useAuth()`** — `{ user, isLoading, login, logout, refresh }`. Single context for now.

If we add billing / tier-gating, split into smaller hooks (`useTier`, `useHasFeature`) that compose `useAuth` — don't grow a catch-all that returns everything; that pattern causes unrelated re-renders.

## Layout chrome

- **`RootLayout`** owns root flex, scrollable `<main>`, scroll-to-top, error boundary, footer, and `<Toaster />`.
- **`Nav`** is the top bar. Footer suppression is currently a hardcoded `HIDE_FOOTER` set in `root-layout.tsx` — when route count grows, migrate to `createBrowserRouter` route handles (see monk's pattern).
- **`<PageContainer>`** wraps page content with the canonical shell (`max-w-[1400px] mx-auto px-4 sm:px-6 pt-6 pb-20 fade-up`). Pages call it as their root.
- **`<AuthShell>`** is the layout primitive for auth flows (login, signup). Vertically + horizontally centers a narrow content column.

## Component primitives

- **shadcn primitives live in `web/src/components/ui/`.** Use them; check before writing raw markup.
- **If a primitive is missing**, prefer `bunx shadcn@latest add <component>` over hand-rolling.
- **Compose, don't subclass.** Use `<Button asChild>` (Radix slot pattern) when a primitive needs to render as a different element.
- **`className` is always accepted** on reusable components and merges with internal classes via `cn()`. Never override; merge.

## Forms

- **`react-hook-form` + `zod`** for every form. No ad-hoc state.
- **Form primitives** (`<Form>`, `<FormField>`, `<FormItem>`, `<FormLabel>`, `<FormControl>`, `<FormMessage>`) wire ARIA attributes automatically. Use them; don't render raw `<label>` + error `<p>`.

## Mobile responsiveness

- **Tables → cards at sub-`md:`.** Build a `md:hidden` card list and `hidden md:block` table side-by-side.
- **Multi-column layouts stack** on mobile. Don't horizontally scroll page-level layouts.
- **Tap targets ≥ 44×44px.** Don't shrink interactive controls below this on touch.

## Motion

- **Use specific transitions, never `transition-all`.** Layout-property animations cause jank.
- **Respect `prefers-reduced-motion`** — large entrance animations should skip when reduced motion is set.

## When to add a new pattern

Three cases warrant a new helper / primitive in `web/src/`:

1. **Same shape twice in a sprint** — copy is fine; abstract on the third occurrence (rule of three).
2. **Domain-specific composition** — like `serviceStatusVariant`. Stays in `lib/*-variants.ts`.
3. **Cross-cutting concern that isn't framework-provided** — like a `useMediaQuery` hook.

Avoid abstracting for the future. Write the third occurrence first, then extract.
