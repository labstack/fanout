# Agent dashboards — milestone 3 (Authoring quality) implementation plan

> **For agentic workers:** Execute the fifteen dispatches in order (1a, 1b, then 2–14), with one controller review gate per task. Steps use checkbox (`- [ ]`) syntax. Codex edits files and reports results; the controller reviews and commits. Task 1a builds the renderer/app against fixtures; Task 1b cuts over the server and removes the second workspace. They run back-to-back, each with a review gate, followed immediately by Task 2 zero legacy. Reports are task-1a-report.md, task-1b-report.md, then task-2-report.md through task-14-report.md; downstream task labels stay fixed for the controller dispatches.

**Goal:** Make dashboard authoring measurable and dependable: identical panels in chat and dashboards, no retired product contracts, a quiet live build receipt and precise edit chips, history/restore, full-screen and Explain, and keyboard interaction. Demonstrate S1–S5 on an immutable demo source replayed near the run’s current time.

**Architecture:** One Bun workspace, `ui/host`, builds the browser SPA and an additional single-file MCP app. Package-free `ui/panels` remains the spec/frame compiler; React rendering stays in `ui/host/src/dashboards`. MCP returns dashboard fragments through the existing panel executor. The app communicates only through the MCP bridge, inside `sandbox="allow-scripts"`. SQLite retains owner-scoped immutable dashboard versions; DuckDB retains telemetry, rollups and annotations. Receipt data comes from executed tools and saved version diffs, never model narration.

**Tech Stack:** Go 1.27.1 (the current `go.mod`/Docker toolchain), Echo v5, pinned DuckDB 2 via `scripts/with-duckdb.sh`, modernc SQLite + Goose + sqlc, MCP Go SDK, AG-UI, React 19.3, Mantine 9.6.3, ECharts 6.1 canvas, Dagre 3.1.1, react-grid-layout 2.2.4, TanStack Router/Query/Table, Bun 1.4.2, Vite 8.3.1, Vitest 5.0.3.

**Spec:** `docs/superpowers/specs/2026-10-04-agent-dashboards-design.md`. Binding brief and R1–R4: `.superpowers/sdd/2026-10-08-agent-dashboards-m3/plan-brief.md`. Format precedent: `docs/superpowers/plans/2026-10-05-agent-dashboards-m2.md`. Baseline evidence: `docs/benchmarks/2026-10-agent-dashboards-m2.md`. Surveyed clean branch `feat/agent-dashboards-m3`, tracking merged M2 on main. Existing paths and functions below were read from this tree; files marked **Create** and symbols explicitly described as new are proposed additions.

## Global Constraints

- Work in this worktree on `feat/agent-dashboards-m3`. No git writes by Codex, pushes, merges or deployment. A controller review/commit handoff ends each task; there are no Codex commit commands.
- Follow `.superpowers/sdd/2026-10-08-agent-dashboards-m3/codex-rules.md`: write a failing behavior test, record its failure, implement, record its pass, report files/steps/deviations/test names and verification in the task's `task-N-report.md`. Never ask implementation questions; record choices. A denied action means BLOCKED with the exact action, without an alternate execution path.
- Read code with plain `rg`, `sed -n` and `cat`, as that process file explicitly requires. No MCP, CodeGraph initialization, browser or Playwright tools by Codex. Commands below use RTK for execution; browser launch and hands-on checks are controller handoffs. Syntax/assertion tests for the collector do not launch a browser.
- Before **any** Go command, from the repository root:

  ```bash
  rtk proxy mkdir -p .superpowers/gocache .superpowers/gotmp
  export GOCACHE="$PWD/.superpowers/gocache"
  export GOTMPDIR="$PWD/.superpowers/gotmp"
  export GOFLAGS=-mod=readonly
  export GOPROXY=off
  ```

  Use `rtk just test ./real/package/...`; never bare `go test`. Quote alternation through Just as `-run '"TestA|TestB"'`. Every Go task ends with `rtk just fmt`. Every contract/route/tool change runs `rtk just docs-generate` and `rtk just docs-generate-check`.
- Except Task 1a’s explicitly staged app build, every UI task runs `rtk proxy sh -c 'cd ui/host && bun run test'`, `rtk proxy sh -c 'cd ui/host && bun run lint'`, then `rtk just ui`. Include both embedded output directories in the controller handoff. Task 1b replaces `ui-check` with a temporary-output comparison so it neither requires committed assets nor restores the tree with git. Final `just check` is then safe on an uncommitted tree.
- Models: agent default `claude-sonnet-5-5`, OpenAI default `gpt-6.1-sol`; never Haiku. Controller supplies authenticated disposable local instances and runtime-injected provider keys. No telemetry, cookies, tokens, provider response bodies, prompts from the holdout, or raw evaluation artifacts in git.
- Paid evaluation caps are independent per task, never an authorization to spend beyond the stated cap. Task 3 is mock-only ($0); Task 12 ≤ $5; Task 14 ≤ $5. Meter every provider call into a running ledger. Before each prompt, stop when spent + measured p95 completed-prompt cost (or $0.60 before any measurement) would exceed that task cap. Preserve the runtime’s 16-step limit; missing usage stops subsequent prompts. The controller asks the user before a larger run. No paid judges are necessary for S1–S5.
- Holdout is sealed (sha256 prefix `a74ce656679ca792`): never read its prompts to author/tune the plan, tools or prompt. Only the controller's final frozen-candidate invocation may open it; aggregate results cannot be used to tune this candidate. A missing or incomplete run is missing evidence, not a pass.
- Public docs and plans use repository-relative paths. No demo VM IP, jump host or local home-directory paths. Runtime locations are environment values supplied privately by the controller. Existing historical plans are not rewritten to make a source-code search appear clean.
- Preserve AGENTS.md invariants, with the explicit Task 2 reader-less cache policy edit below: control state remains database/sql + modernc SQLite, Goose migrations are immutable, sqlc queries are the single control-schema path; schema/query changes require `just db-gen` and storage tests. Task 6 adds one forward migration for cross-thread build provenance; extend the checksum ledger without replacing published checksums. Preserve format 3, typed attributes/VARIANT, UTC/nanosecond predicates, snapshot pinning, complete-batch cache/compaction semantics, disjoint gates/tables and bounded rooted traversal. Retain every cache with a live reader; delete endpoint/log read-cache contributions after verifying their last readers are removed, bump the disposable schema version and update the corresponding AGENTS.md sentences in Task 2.
- New product JSON uses snake_case; AG-UI/MCP protocol fields keep their protocol spelling. No aliases or old-shape decoder. Public MCP replacement tools use `replace`, new tools are verb-first snake_case. Trace route is `GET /api/traces/:id`; existing annotations remain POST, the M2 binding decision. Product CalVer is independent of spec/contract versions.
- Keep all fifteen visualization types, worst-first/confidence selection, six validated palette slots, safe tooltip escaping, full-window reducers, partial/stale/error/empty states, linked charts, drill cancellation and S8 batching. S6/S7 optimization and full-board S13 comparison remain M4.

## Recorded rulings and scope decisions

1. **R1 resolves the renderer wording.** `ui/panels` owns package-free compile/model logic, while the one host workspace owns React visualization rendering. Delete `ui/apps` after its shell/bridge move. One generic `panels.html` can display a fragment and the shared trace-detail component; no separate `trace.html` is necessary. Trace waterfall remains an explicit optional detail alongside a `traces` panel, not a sixteenth v1 visualization.
2. **Move retirement forward from M4 to M3**, as Task 2 requires. Keep active `internal/observability` Overview/Topology/Dependencies/Trace services behind panel/MCP adapters. Replace the health panel's expensive `Performance` call with a purpose-built error-trend read, then remove Performance/Logs adapters and their now-unreachable SQL. Controller I3 ruling: delete endpoint-histogram and log-count contributions, snapshot sources and read kinds after the last readers retire; increment readCacheVersion from 3 to 4 and update AGENTS.md. Keep notable-trace candidates and every cache with a live reader; if execution-time rg finds an additional reader in intelligence, alerts, MCP or panels, retain that cache and record the caller and reason. No trace repository rewrite or M4 routing optimization is included.
3. **Log-pattern context is already implemented after M2.** `compileRowsWhere` returns dominant severity/top service, and `TestPreviewPatternContextEngine` executes redaction, ties and trends. Retain derived columns and `by:[body_template]`; do not add optional grouping or bump v1. Task 11 expands scope/tie coverage and documents the meaning.
4. **Shortcut keys are not specified by the spec.** Use scoped `r` refresh, `e` layout mode, `h` history, `f` focused-panel full-screen, `?` shortcut help; Escape closes the top dialog via Mantine. Existing `/` chat composer remains global. Ignore inputs, contenteditable, modifier chords, repeats and events from an open modal/drawer.
5. **Five consecutive S5 edits replace the throwaway harness's one-add edit.** Compare immediate before/after specs after every edit; allow server-packed grid changes only for add/remove/move. Content changes may not alter other panels' authored fields, variables or dashboard time. Ten creates plus five edits must run on one recorded near-now replay of the immutable source and the default model; null metrics and unchecked validation fail.
6. **Fix two cheap M2 minors; defer qualified log-column SQL.** Empty multi-select URL encoding and a separate version-rollup gate-wait timeout are local changes with behavioral tests. Schema-qualified log column rewriting needs AST scope/alias binding across nested queries; keep its fail-closed behavior and list it for M4 rather than risking redaction in this milestone.
7. **Revision 2 controller rulings:** plan-fix.md governs C1, I1–I9 and M1–M21. Tasks 1a/1b are separate review gates with the verbatim seam above; Task 2 stays zero legacy immediately afterward. Exact pins are knip 6.38.0 and deadcode v0.50.0. Model summaries are <=16 KB; persisted full fragments are <=256 KB with visible row truncation, never re-query-on-reload. Dashboard Explain enforces answer_only, chat hides it, manager-only error fix remains a separate edit. Evals replay near now with recorded shift/source hash and meter each provider call under the p95/$0.60 prompt gate. Holdout outputs are controller-only outside the worktree.


## Review Focus

1. Tasks 1a/1b: a frozen fixture-tested interface precedes the atomic cut-over; one visualization implementation; MCP resources are fully self-contained, worker inline, no SPA router/auth imports in the app bundle; iframe cannot fetch the host API. Contract changes update Go, AG-UI activity, TypeScript, bridge, tests and generated docs together.
2. Task 2: every removed route/file/symbol has a replacement below. Keep shared health/trace/query dependencies. Pinned knip checks files/exports with tests excluded as entry roots; module-wide Go `unused` has no cmd exemption, and pinned whole-program deadcode excludes tests as roots over all cmd mains. Failure injection proves unused files/exports, exported Go dead code and exported test-only references fail.
3. Tasks 4–6: intermediate model text never reaches live or reloaded answers. Final/refusal/truncation/error outcomes remain visible. Receipt corrections are observed transitions, saved changes use committed version content, and failed mutations never masquerade as success.
4. Tasks 7–8: history and restore remain owner-scoped, show person/agent and message, restore adds a version, stale cache is invalidated, double-click cannot produce duplicate restore submissions.
5. Tasks 9–10: full-screen reuses the same result without an extra query, Explain pins an observed absolute panel window and variables and does not request mutation, keyboard selection travels through the same `Selection` callbacks as mouse selection.
6. Task 11: pattern context remains redacted/scoped and bounds trends, timeout includes waits without consuming the ten-second execution budget, URL distinguishes All, empty, empty-string, single and multiple values.
7. Tasks 3/12/14: frozen source hash/replay shift/time bounds, complete SSE, persisted save/version evidence, executed panel status, all five diffs, actual cost and model identity; no silent retry of a mutating agent POST. Final collector tests and Chrome plus Safari/WebKit evidence are actual observations, not fixtures or API-only substitutes for rendering.

## Task 1a: Shared renderer and fixture-tested single-file app in host

**Files**
- Modify: `ui/host/package.json`, `ui/host/bun.lock`, `ui/host/src/dashboards/drill.tsx`, `ui/host/src/dashboards/page.tsx`, `ui/host/src/dashboards/panel-card.tsx`, `ui/host/src/dashboards/viz/service-map.tsx`, `ui/host/src/mcp-app-frame.tsx`, `ui/host/src/mcp-app-frame.test.tsx`, `ui/host/src/dashboards/use-variables.ts`, `ui/host/src/dashboards/variable-bar.tsx`.
- Create: `ui/panels/fragment.ts`, `ui/panels/variables.ts`, `ui/host/panels.html`, `ui/host/vite.apps.config.ts`, `ui/host/src/mcp-apps/{main.tsx,use-panel-app.ts,app.css,transport.ts,transport.test.ts}`, `ui/host/src/dashboards/{fragment-view.tsx,fragment-view.test.tsx,drill-client.ts}`, `ui/host/src/dashboards/trace/{detail.tsx,detail.test.tsx}`, `ui/host/src/mcp-apps/build-graph.test.ts`.

Budget: $0. TypeScript/build only; no Go server or activity cut-over. `ui/apps` and its five outputs remain registered and still serve chat until Task 1b. Build the new app to `.superpowers/build/m3-apps` (staging), leaving existing embedded outputs intact. Keep existing git-based ui-check ownership unchanged in this task; Codex does not invoke its git-restoring recipe. Existing ui-boundaries applies while both workspaces exist; do not import across them.

**Frozen interface for Task 1b (copy verbatim into its implementation report; no unilateral seam changes):**

- `PanelFragment` JSON is `{dashboard: DashboardSpec, results: PanelResult[], vars?: Record<string,string|string[]>, trace?: Result<TraceDetail>}`. Product fields inside all these types are snake_case. Results match every dashboard panel id exactly once; no old view payload decoder. `$__all`, scalar, empty list and multi list are preserved.
- `query_telemetry` takes `{panel, variables?, time?, vars?}`; model+app visible, resource `ui://fanout/panels.html`.
- `query_panel_fragment` takes `{dashboard, time?, vars?, widths?, compare?}`; app-only, no resource attachment. The `panels` key is forbidden, including empty/null values; the server rejects it before execution so the full-fragment invariant is maintained.
- `get_panel_exemplars` takes `{dashboard, panel_id, kind?, time?, from, to, dimensions?, bucket?, vars?}`; app-only, returns the existing exemplar response.
- `resolve_panel_variables` takes `{dashboard, time?, vars?}`; app-only, returns `{options: Record<string,Option[]>}`.
- `inspect_trace` takes `{trace_id?, service?, namespace?, window?, from?, to?, limit?}`; model+app visible, resource `ui://fanout/panels.html`. `from` and `to` are RFC3339Nano, both-or-neither, exclusive with `window`; the app sends exact `trace_id`, `namespace`, `from`, `to`, `limit:200`. It returns a fragment with optional trace detail.
- AG-UI activityType stays `mcp-app`; content keys are exactly `{resource_uri, tool_name, tool_input, tool_result, is_error}`. Protocol `structuredContent`, `resourceUri`, `toolCallId`, `threadId`, `runId` retain their spelling. `tool_result` is the same bounded full fragment as MCP structuredContent, persisted for reload without a new query.
- Model text comes only from deterministic `fragmentSummary`: panel id/status/row count/truncated/diagnosis or error/top 5 labelled rows or values, at most 16 KB. Full serialized fragments are at most 256 KB after row truncation with a visible note. Preset limits are logs 100, traces 50, health/service_map 400.
- Chat fragments hide Explain, Copy link for unsaved fragments, and every mutating action. Saved-dashboard Explain and its separate fix action are Task 9; there is no iframe Explain contract.

- [ ] **Step 1: Fail renderer, decoder and transport fixture tests**

Create `ui/host/src/dashboards/fragment-view.test.tsx`:

```tsx
import { act } from "react";
import { createRoot } from "react-dom/client";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { expect, it, vi } from "vitest";
import { FragmentView } from "./fragment-view";
import type { PanelFragment } from "../../../panels/fragment";

vi.mock("./echart-canvas",()=>({EChartCanvas:({label}:{label:string})=><div role="img" aria-label={label}/> }));
it("uses PanelCard for spec/data/empty states and never fetches the host",async()=>{
  const fetch=vi.spyOn(globalThis,"fetch");
  const fragment:PanelFragment={dashboard:{version:1,name:"Answer",time:{range:"1h",refresh:"off"},panels:[{id:"logs",title:"Checkout logs",viz:"logs",query:{from:"logs"}}]},results:[{id:"logs",status:"empty",diagnosis:"No logs for checkout",elapsed_ms:1,from_ms:0,to_ms:3600000}]};
  const el=document.createElement("div");document.body.append(el);const root=createRoot(el);
  const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  const drill={exemplars:vi.fn(),trace:vi.fn()};
  try {
    await act(async()=>root.render(<MantineProvider><QueryClientProvider client={client}><FragmentView fragment={fragment} dark={false} drillClient={drill} onQuery={vi.fn()} /></QueryClientProvider></MantineProvider>));
    expect(el.textContent).toContain("No logs for checkout");
    expect(el.querySelector('[data-panel-view="Data"]')).not.toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  } finally {await act(async()=>root.unmount());el.remove();fetch.mockRestore();client.clear();}
});
```

Implement `ui/panels/fragment.ts` after the fixture decoder tests fail (package-free):

```ts
import type { Result, TraceDetail } from "../contracts";
import type { DashboardSpec, PanelResult, VarValue } from "./types";

export type PanelFragment={
  dashboard:DashboardSpec;
  results:PanelResult[];
  vars?:Record<string,VarValue>;
  trace?:Result<TraceDetail>;
};

export function panelFragment(value:unknown):PanelFragment {
  if(!value || typeof value!=="object") throw new Error("Missing panel view");
  const f=value as Partial<PanelFragment>;
  if(f.dashboard?.version!==1 || !Array.isArray(f.dashboard.panels) || !f.dashboard.panels.length || f.dashboard.panels.length>40 || !Array.isArray(f.results)) throw new Error("Invalid panel view");
  const ids=new Set(f.dashboard.panels.map(p=>p.id));
  if(ids.size!==f.dashboard.panels.length || f.results.length!==ids.size || new Set(f.results.map(r=>r.id)).size!==ids.size || f.results.some(r=>!ids.has(r.id)||!["ok","empty","error"].includes(r.status))) throw new Error("Panel results do not match the view");
  if(f.vars && Object.values(f.vars).some(v=>typeof v!=="string" && !(Array.isArray(v)&&v.every(x=>typeof x==="string"))))throw new Error("Invalid panel variables");
  return f as PanelFragment;
}
```

Add fixtures for all five presets, query_telemetry, errors/empty/truncated results, trace detail and string/list variables. Assert chat fragments never show Explain, Ask Fanout to fix it, Duplicate, Remove or unsaved Copy link in normal/error/full-screen states. Assert decoder rejects duplicate/missing/subset ids and old payload shapes. Add transport tests before its implementation; mock App only, no live server assumption.

```bash
rtk proxy sh -c 'cd ui/host && bun run test src/dashboards/fragment-view.test.tsx src/mcp-apps/transport.test.ts'
```

Expected RED: missing modules. Record failures before implementing.

- [ ] **Step 2: Share renderer, trace detail, variables and bridge transport**

Introduce required `DrillClient` (Create `drill-client.ts`):

```ts
import type { Result, TraceDetail } from "../../../contracts";
import type { ExemplarBody, ExemplarResponse } from "./api";
import type { DrillTarget } from "./drill-state";
export type DrillClient={
  exemplars(body:ExemplarBody,signal?:AbortSignal):Promise<ExemplarResponse>;
  trace(target:DrillTarget,signal?:AbortSignal):Promise<Result<TraceDetail>>;
};
```

Change `DrillDrawer` to require `client:DrillClient`; replace its calls to `queryExemplars` and `getTrace` with `client.exemplars` and `client.trace`. DashboardPage passes `{exemplars:queryExemplars,trace:getTrace}` as a stable module-level object. Move the existing shared waterfall/logs/group/truncation JSX from `drill.tsx` into `trace/detail.tsx` `TraceDetailView({result,dark,onSpan})`; use it from both drill and FragmentView. Port the old app’s unique FlameGraph with its assertions into this shared detail; Task 1b deletes its old copy. Extract currentValue to ui/panels/variables.ts, remove hook/bar re-exports and update all consumers/test imports so only one definition remains. Retain the existing EmptyState and paging helpers in `dashboards/trace`; remove only their copied-source comments in Task 2. Do not copy trace UI again.

Create `transport.ts` using the bridge, with explicit value conversion:

```ts
import type { App } from "@modelcontextprotocol/ext-apps";
import type { PanelFragment } from "../../../panels/fragment";
import { panelFragment } from "../../../panels/fragment";
import type { QueryBody } from "../dashboards/api";
import type { DrillClient } from "../dashboards/drill-client";

export function appTransport(app:App) {
  async function call<T>(name:string,args:Record<string,unknown>,signal?:AbortSignal):Promise<T> {
    if(signal?.aborted) throw new DOMException("Canceled","AbortError");
    const result=await app.callServerTool({name,arguments:args},{signal});
    if(signal?.aborted) throw new DOMException("Canceled","AbortError");
    if(result.isError || !result.structuredContent) throw new Error("This view could not be refreshed.");
    return result.structuredContent as T;
  }
  const drill:DrillClient={
    exemplars:(body,signal)=>call("get_panel_exemplars",{...body},signal),
    trace:async(target,signal)=>{
      const fragment=await call<PanelFragment>("inspect_trace",{trace_id:target.trace_id,namespace:target.namespace,from:target.window_from,to:target.window_to,limit:200},signal);
      if(!fragment.trace) throw new Error("Trace unavailable");
      return fragment.trace;
    },
  };
  return {
    drill,
    query:async(body:Omit<QueryBody,"panels">)=>{
      if (Object.hasOwn(body,"panels")) throw new Error("Panel subsets are unavailable in chat fragments");
      const raw=await call<PanelFragment>("query_panel_fragment",{...body});
      const next=panelFragment(raw);
      next.vars=body.vars;return next;
    },
  };
}
```

Add `transport.test.ts` with an App-shaped mock: query rejects a raw panels key (including empty/null); query sends one `query_panel_fragment` request, multi/All/empty values remain strings/lists, signal is passed in SDK RequestOptions, canceled trace results are ignored, tool errors reject, no fetch. Installed ext-apps 2.0.3 `App.callServerTool` accepts RequestOptions; use its signal and bridge's existing `extra.mcpReq.signal`. Keep a late-result guard even with cancellation; executor deadlines bound remote work.

Create `FragmentView` as a layout/interaction adapter, with required `onQuery(body:Omit<QueryBody,"panels">):Promise<PanelFragment>` and `drillClient`; it renders existing `PanelCard` for every panel, existing `VariableBar` for custom/constant/text variables, and `DrillDrawer` with local target state. It owns display variables and absolute zoom time, and invokes one batch query on variable/zoom/refresh changes. Resolve query-variable options through a new app-only MCP adapter over `Executor.ResolveVariables` before rendering the bar; reuse `VariableBar` rather than making another picker. Use result `from_ms/to_ms` in `makeDrill`; do not use Date.now for a clicked result. Keep fetched old rows labeled stale while refreshing and expose failures. A new generation ignores previous refresh completions; closing a drill never restores it. PanelCard props: `group` unique via React `useId`, `height` 360 by default, `loading` from local pending, `editing=false`, `agentAvailable=false`, `onView` opens one full-screen shared card, `onCopyLink` is absent for unsaved fragments. Chat fragments hide Explain and every mutating action in menu/error/full-screen; no App.sendMessage Explain callback. Set onSelect/onPoint using the same grid precedence (avoid setting a variable twice when onPoint handles it). Use the same compiler/palette/safe text paths; no app-local `echarts` option builder.

- [ ] **Step 3: Build the isolated shell and enforce its import/worker boundaries**

Move `useFanoutApp`’s connection/host-context/error plumbing from the still-live app into new `use-panel-app.ts`; replace generic blind casting with `panelFragment`, validate vars as strings/lists, register no old payload decoder. Rename the hook to `usePanelApp` to distinguish the host's `useFanoutApp`. `main.tsx` creates one QueryClient and a MantineProvider using existing `fanoutTheme`/`fanoutCssVariables`, reacts to `host.theme` changes through `forceColorScheme`, imports Mantine styles and the same two Geist fonts. Before bridge/app/fragment are ready, render a Loader or sanitized error. Once ready render FragmentView with `appTransport(app)`, trace detail if present, and no Explain/fix/mutating callbacks. Do not import `main.tsx`, router, App, browser auth or provider context into this entry. Keep app-only CSS limited to html/body/root sizing; import existing dashboard CSS needed for PanelCard and table appearance.

Create `ui/host/panels.html`:

```html
<!doctype html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Fanout panels</title></head><body><div id="root"></div><script type="module" src="/src/mcp-apps/main.tsx"></script></body></html>
```

Create `ui/host/vite.apps.config.ts`:

```ts
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";
export default defineConfig({
  plugins:[react(),viteSingleFile(),{
    name:"strip-trailing-whitespace",enforce:"post",
    generateBundle(_options,bundle){for(const output of Object.values(bundle)){if(output.type==="asset" && typeof output.source==="string") output.source=output.source.replace(/[ \t]+$/gm,"");}},
  }],
  publicDir:false,
  build:{outDir:process.env.FANOUT_APPS_OUT??"../../.superpowers/build/m3-apps",emptyOutDir:true,assetsInlineLimit:100_000,cssMinify:true,minify:true,rollupOptions:{input:"panels.html"}},
});
```

Move `vite-plugin-singlefile` **2.3.3** to host devDependencies; ext-apps **2.0.3 is already in host**, so do not duplicate it. Cross-env is unnecessary: Bun runs on supported native systems and the new config has a fixed entry. Add host scripts `build:host="bun --bun vite build"`, `build:apps="bun --bun vite build --config vite.apps.config.ts"`; in 1a leave the existing `build` host-only so old chat outputs and the audit build keep working. Task 1b changes `build` to `"tsc --noEmit && bun run build:host && bun run build:apps"` and SPA Vite `outDir` to `process.env.FANOUT_UI_OUT ?? "../../internal/ui/dist"` for staged checks. Keep `build:watch` SPA-only. Install/update the single host lockfile through the controller's available dependency cache (report a denied/missing dependency, do not substitute a version).

In Task 1a, change service-map worker creation to import `ServiceMapWorker from "./service-map.worker?worker&inline"` and `new ServiceMapWorker()`. Both SPA and app consume the same component; 400-node layout still executes off-thread. Add `worker-src blob:` to `mcpAppCSP`; keep connect-src 'none', default-src 'none', inline fonts/styles and sandbox allow-scripts only. Add CSP and >60-node app tests; never enable allow-same-origin, remote worker scripts or host fetch.

Add an app-build Rollup plugin in vite.apps.config.ts: walk resolved runtime imports transitively from `src/mcp-apps/main.tsx` via `getModuleInfo`; fail build if reachable modules include host auth, router, routes/, App.tsx/main.tsx, app/provider context, or runtime/value imports of dashboards/api.ts. Type-only API contracts disappear before this graph and are permitted. Test direct and transitive forbidden imports in temporary fixture graphs and a permitted shared-renderer graph; no review-only boundary.

Assert the emitted app worker constructs a `blob:` URL and calls Worker with it, rather than Vite’s data: fallback. Test the mocked Blob/URL/Worker path, forbid data: Worker construction under the CSP, and catch initialization/layout failure with the existing visible map layout-error state. Supported-browser proof is Task 14 Step 3 in Chrome and Safari (WebKit via Playwright), including opaque-origin sandbox behavior.

- [ ] **Step 4: Green fixture gates and measure staged panels.html**

```bash
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk proxy sh -c 'cd ui/host && FANOUT_APPS_OUT=../../.superpowers/build/m3-apps bun run build:apps'
rtk proxy wc -c .superpowers/build/m3-apps/panels.html
rtk proxy sh -c 'gzip -9 -n -c .superpowers/build/m3-apps/panels.html | wc -c'
```

Assert staged output path set is exactly `{panels.html}` and all JS/CSS/fonts/worker code is inline. Test panels.html against fixture fragments, both themes, using Vitest; these tests are not live chat acceptance. Record measured size and frozen contract in task-1a-report.md. Controller reviews the renderer/build slice before dispatching 1b; no Go dependency or old output deletion in 1a.

## Task 1b: Go fragments, bounded model content and atomic activity/workspace cut-over

**Files**
- Modify: `internal/mcp/{apps.go,server.go,panels.go,server_test.go}`, `internal/agent/{tools.go,runtime.go,runtime_test.go,tools_integration_test.go,store_test.go}`, `ui/host/{package.json,bun.lock,vite.config.ts,vite.apps.config.ts}`, `ui/host/src/{mcp-app-frame.tsx,mcp-app-frame.test.tsx,chat.tsx,app-context.ts}`.
- Create: `internal/mcp/{fragments.go,fragments_test.go}`, `internal/agent/fragment_content_test.go`, `scripts/{ui-compare.mjs,ui-compare.test.ts}`.
- Modify: `justfile`, `Dockerfile`, `.dockerignore`, `lefthook.yml`, `.github/dependabot.yml`, `README.md`, `CONTRIBUTING.md`, `scripts/{ui-boundaries.mjs,ui-boundaries.test.ts}`, `internal/cmd/notices/{main.go,main_test.go}`, `internal/ui/ui.go`, `site/src/components/Icon.astro`, generated `THIRD_PARTY_NOTICES`, generated HTTP/MCP references.
- Private Modify: `.superpowers/replay/build-audit.sh` (git-ignored controller build, repaired here).
- Delete: all `ui/apps/` package/lock/config/sources/HTML and the old five embedded HTML outputs after the generic output builds. Keep only `internal/mcp/apps/panels.html` and its registration.

Budget: $0. Implement the frozen Task 1a interface verbatim. Seeded actual MCP calls supply both-theme chat acceptance without paid model calls. Task 2 follows immediately.

- [ ] **Step 1: Fail executable preset, cap and model/activity contract tests**

Create `internal/mcp/fragments_test.go` (package helpers `newPanelServer`, `ownerRequest` already exist):

```go
package mcp

import (
    "encoding/json"
    "testing"
    "github.com/labstack/fanout/internal/panel"
)

func TestQueryTelemetryReturnsExecutableFragment(t *testing.T) {
    s := newPanelServer(t)
    p := panel.Panel{ID:"latency", Title:"Latency", Viz:"timeseries", Query:&panel.Query{From:"spans", Measures:[]string{"p95(duration_ms)"}, Bucket:"auto"}}
    _, got, err := s.queryTelemetry(t.Context(), nil, QueryTelemetryInput{Panel:p})
    if err != nil { t.Fatal(err) }
    if got.Dashboard.Version != 1 || len(got.Dashboard.Panels)!=1 || len(got.Results)!=1 || got.Results[0].ID!=p.ID { t.Fatalf("fragment=%+v", got) }
    raw, err := json.Marshal(got); if err != nil { t.Fatal(err) }
    var decoded map[string]any; if err=json.Unmarshal(raw,&decoded); err!=nil {t.Fatal(err)}
    if _, ok:=decoded["data"]; ok {t.Fatal("bespoke view data returned")}
    if _, ok:=decoded["dashboard"]; !ok {t.Fatal("missing spec")}
    if got.Results[0].Status==panel.StatusEmpty && got.Results[0].Diagnosis=="" {t.Fatal("unexplained empty")}
}

func TestFragmentPresetsUseSingleUnitPanels(t *testing.T) {
    for _, name:=range []string{"overview","performance","topology","logs","trace"} {
        d:=fragmentPreset(name, "checkout", "shop", "ERROR", "timeout", 20)
        if len(d.Panels)==0 {t.Fatalf("missing %s preset",name)}
        for _, p:=range d.Panels {
            if p.ID=="latency_error_correlation" {t.Fatal("retired dual-axis panel")}
            if p.Viz=="timeseries" && len(p.Query.Measures)>1 {t.Fatalf("split incompatible measures: %+v",p)}
        }
        s:=newPanelServer(t)
        results,err:=s.panels.Run(t.Context(),panel.RunRequest{Dashboard:d});if err!=nil {t.Fatalf("%s: %v",name,err)}
        for _,r:=range results {if r.Status!=panel.StatusOK && !(r.Status==panel.StatusEmpty && r.Diagnosis!="") {t.Fatalf("%s panel %s: status=%s diagnosis=%q error=%q",name,r.ID,r.Status,r.Diagnosis,r.Error)}}
    }
}
```

Update resource/metadata integration assertions: all five view tools and `query_telemetry` advertise `ui://fanout/panels.html` only when Apps is negotiated; an ordinary MCP client still gets the compact deterministic summary plus identical bounded structured results; app-only helpers are omitted from its catalog. Verify the resource's declared MIME, inlined scripts/fonts/styles, no external JS/CSS/worker assets, no flat metadata alias, and immutable catalog TTL. The generic app must reject malformed fragment payloads visibly instead of decoding any of the old `Result<Overview|Performance|Topology|Logs>` shapes.

Extend the preset test before implementation: seed spans/logs and service/edge rollups; assert every returned result is `ok`, or `empty` with a nonempty diagnosis, never merely that Run has no top-level error. The sample loop checks result statuses; seed its fixture and additionally require a positive result for each preset. `newPanelServer` already calls SetRollupReader(observability.New(...)); preserve that wiring in the seeded helper. Include real http_route values and empty routes and assert endpoints use promoted http_route and exclude empty routes.

Add `TestFragmentSummaryBounded`, covering all five executed presets and a 1000-row logs frame of 2000-character bodies, wide columns, Unicode and 40-panel fragments. Assert summary bytes <=16*1024, deterministic output, required metadata, at most five labelled rows per panel, no SQL or full rows in provider-facing content. Add `TestFragmentPayloadCap` with >256*1024 serialized bytes: call succeeds, serialized full fragment <=256*1024, largest frames lose rows, column lengths/Rows/trends remain consistent, truncated=true and a visible note survive decoder/rendering. Cover previous frames and optional trace spans/logs, plus already-small fragments unchanged.

Complete large-log fixture regression in fragments_test.go (alongside executed preset tests):

```go
func largeLogsFragment() PanelFragment {
    d:=fragmentPreset("logs","checkout","shop","ERROR","",100)
    f:=&panel.Frame{Columns:[]panel.Column{{Name:"service",Type:"string",Role:"dimension"},{Name:"body",Type:"string",Role:"dimension"}},Values:[][]any{{},{}},Rows:1000}
    for i:=0;i<f.Rows;i++ {f.Values[0]=append(f.Values[0],"checkout");f.Values[1]=append(f.Values[1],strings.Repeat("x",2000))}
    return PanelFragment{Dashboard:d,Results:[]panel.Result{{ID:"volume",Status:panel.StatusEmpty,Diagnosis:"No volume buckets in fixture"},{ID:"events",Status:panel.StatusOK,Frame:f}}}
}
func TestFragmentSummaryBoundedLogsFixture(t *testing.T) {
    f:=largeLogsFragment();a:=fragmentSummary(f.Results);b:=fragmentSummary(f.Results)
    if len(a)>16*1024 || a!=b {t.Fatalf("summary bytes=%d deterministic=%v",len(a),a==b)}
    for _,label:=range []string{"events","ok","1000","truncated","checkout"} {if !strings.Contains(a,label) {t.Fatalf("missing %q",label)}}
    if strings.Contains(a,strings.Repeat("x",2000)) {t.Fatal("unclipped body escaped into model text")}
}
func TestFragmentPayloadCapLogsFixture(t *testing.T) {
    original:=largeLogsFragment();got:=boundFragment(original)
    raw,err:=json.Marshal(got);if err!=nil {t.Fatal(err)}
    f:=got.Results[1].Frame
    if len(raw)>256*1024 || !f.Truncated || f.Note=="" || f.Rows>=1000 {t.Fatalf("bytes=%d frame=%+v",len(raw),f)}
    for _,values:=range f.Values {if len(values)!=f.Rows {t.Fatal("column length mismatch")}}
    if original.Results[1].Frame.Rows!=1000 {t.Fatal("mutated source fixture")}
}
```

Add strings to this test file’s imports. Extend to largest-frame selection, previous/trend/trace row consistency and executed seeded presets as specified; the synthetic logs regression checks payload boundaries, not SQL correctness.

Add registry/runtime/store tests: model receives textContent for an app tool even with structuredContent; the persisted activity retains the full bounded fragment and reloaded chart uses the same bytes without querying again. Non-app structured marshal failure remains a tool error. Assert summary persisted as the tool message, not full frames duplicated there. Add non-Apps tools/list exclusion and iframe attempted-dashboard-mutation bridge tests in Step 4.

```bash
rtk just test ./internal/mcp/... -run '"TestQueryTelemetry|TestFragmentPresets|TestFragmentSummary|TestFragmentPayload"'
rtk just test ./internal/agent/... -run '"TestFragment|TestAppTool"'
```

Expected RED: missing Go fragment helpers and old model/activity behavior.

- [ ] **Step 2: Implement presets, fragmentSummary and bounded full payload**

Create Go `PanelFragment` with `Dashboard panel.Dashboard`, `Results []panel.Result`, `Vars map[string]panel.Value`, and `Trace *observability.Result[observability.TraceDetail]`. Task 1b does not add an annotations response field; preserve annotation scope/error in panel results. **Existing `panel.Value.MarshalJSON`/`UnmarshalJSON` use a string, string list or `$__all`, not a Go object.** The TS VarValue shape already matches it; validate values and pass them unchanged. Add round-trip tests for All, scalar, empty and multi values. Use the following new contract/adapters in `internal/mcp/fragments.go`:

```go
type QueryTelemetryInput struct {
    Panel panel.Panel `json:"panel" jsonschema:"One v1 panel to display in the answer"`
    Variables []panel.Variable `json:"variables,omitempty"`
    Time *panel.Time `json:"time,omitempty"`
    Vars map[string]panel.Value `json:"vars,omitempty"`
}
type PanelFragment struct {
    Dashboard panel.Dashboard `json:"dashboard"`
    Results []panel.Result `json:"results"`
    Vars map[string]panel.Value `json:"vars,omitempty"`
    Trace *observability.Result[observability.TraceDetail] `json:"trace,omitempty"`
}
func (s *Server) runFragment(ctx context.Context, req panel.RunRequest) (*mcp.CallToolResult, PanelFragment, error) {
    if s.panels==nil {return nil,PanelFragment{},errors.New("panels are unavailable")}
    panel.Normalize(&req.Dashboard)
    if req.Time!=nil {req.Dashboard.Time=*req.Time;req.Time=nil}
    panel.Normalize(&req.Dashboard)
    results,err:=s.panels.Run(ctx,req)
    if err!=nil {return nil,PanelFragment{},safePanelToolError(err)}
    out:=PanelFragment{Dashboard:req.Dashboard,Results:results,Vars:req.Vars}
    out=boundFragment(out)
    return summary(fragmentSummary(out.Results)),out,nil
}
func (s *Server) queryTelemetry(ctx context.Context, _ *mcp.CallToolRequest, input QueryTelemetryInput) (*mcp.CallToolResult, PanelFragment, error) {
    d:=panel.Dashboard{Name:input.Panel.Title,Variables:input.Variables,Panels:[]panel.Panel{input.Panel}}
    return s.runFragment(ctx,panel.RunRequest{Dashboard:d,Time:input.Time,Vars:input.Vars})
}
func (s *Server) queryPanelFragment(ctx context.Context, _ *mcp.CallToolRequest, input panel.RunRequest) (*mcp.CallToolResult, PanelFragment, error) {
    if input.Panels!=nil {return nil,PanelFragment{},errors.New("panels is forbidden for query_panel_fragment")}
    return s.runFragment(ctx,input)
}
func (s *Server) panelExemplars(ctx context.Context, _ *mcp.CallToolRequest, input panel.ExemplarRequest) (*mcp.CallToolResult, panel.ExemplarResponse, error) {
    if s.panels==nil {return nil,panel.ExemplarResponse{},errors.New("panels are unavailable")}
    out,err:=s.panels.Exemplars(ctx,input)
    if err!=nil {return nil,out,safePanelToolError(err)}
    return summary(fmt.Sprintf("%d exemplar traces.",len(out.Traces))),out,nil
}
```

Imports for this new file: context, encoding/json, errors, fmt, strings, time, unicode/utf8; internal/panel, internal/observability, MCP SDK. Only retain imports used by the completed preset/trace functions below. Register `query_telemetry` (model+app, generic resource), `query_panel_fragment`, `get_panel_exemplars`, and new `resolve_panel_variables` (last three app-only visibility, no UI attachment). Use nested `_meta.ui.visibility:["app"]` for app-only tools. All are read-only telemetry operations and have no dashboard-write scope. `NewToolRegistry` skips app-only tools when building provider `definitions`, but retains the real MCP catalog for AppBridge calls. Add tests that the internal model does not receive app-only definitions and the app client can call them. Reject presence of the raw `panels` input key before typed decoding (including null/empty), expose a schema without that property, and keep the typed non-nil guard as defense in depth. New `resolvePanelVariables` calls `Executor.ResolveVariables(ctx,panel.ResolveRequest)` and returns `{options:map[string][]panel.Option}`, sanitizing errors with safePanelToolError. Extract `currentValue` from `use-variables.ts` into new package-free `ui/panels/variables.ts`; update the hook/VariableBar imports so the iframe does not import host API hooks just to resolve a display value.

The named view tools retain their established behavioral names (not aliases): overview/topology/performance/logs now call `runFragment` over authored presets; trace returns a `traces` panel/result plus optional detail using the existing Trace service. `get_service_dependencies` retains the bounded traversal result because reachability is not one of the retired bespoke charts. Preset construction is complete and deterministic:

Extend existing `QueryInput` and `TraceInput` with optional `From *time.Time`/`To *time.Time`, tags `from`/`to`. Both-or-neither is required; an explicit pair cannot coexist with Window. `s.scope` uses the pair unchanged in UTC and rejects nonpositive/out-of-retention windows through the service/executor. Existing relative Window remains a distinct supported mode, not an old contract alias. Every adapter captures one scope then sets `panel.Time{From:&scope.Start,To:&scope.End,Refresh:"off"}` before running its preset. Trace validates exact id/representative-service mode as today, calls s.queries.Trace, filters the fragment's traces query to the returned trace id/namespace, and attaches the existing detail result. A canceled drill uses the explicit captured from/to fields sent by appTransport. Task 1a already ported the unique FlameGraph into shared trace/detail.tsx with its assertions; this task deletes the old app copy at cut-over, without a second port.

```go
func fragmentPreset(kind, service, namespace, severity, search string, limit int) panel.Dashboard {
    d:=panel.Dashboard{Name:"Telemetry",Time:panel.Time{Range:"1h",Refresh:"off"}}
    filters:=[]string{}
    literal:=func(s string) string {return "'"+strings.ReplaceAll(s,"'","''")+"'"}
    if namespace!="" {filters=append(filters,"namespace = "+literal(namespace))}
    if service!="" {filters=append(filters,"service = "+literal(service))}
    add:=func(id,title,viz,signal,measure,unit string, by []string, bucket string) {
        q:=&panel.Query{From:signal,Where:append([]string(nil),filters...),By:by,Bucket:bucket}
        if measure!="" {q.Measures=[]string{measure}}
        cap:=400
        if viz=="logs" {cap=100};if viz=="traces" {cap=50}
        q.Limit=cap;if limit>0 {q.Limit=min(cap,limit)}
        d.Panels=append(d.Panels,panel.Panel{ID:id,Title:title,Viz:viz,Unit:unit,Query:q})
    }
    switch kind {
    case "overview": add("health","System health","health","spans","","",nil,"")
    case "topology": add("services","Service dependencies","service_map","spans","","",nil,"")
    case "performance":
        add("latency","p95 latency","timeseries","spans","p95(duration_ms)","ms",nil,"auto")
        add("errors","Error rate","timeseries","spans","error_rate()","percent",nil,"auto")
        add("requests","Request rate","timeseries","spans","rate()","per_second",nil,"auto")
        add("endpoints","Slow endpoints","table","spans","p95(duration_ms)","ms",[]string{"http_route"},"")
        d.Panels[len(d.Panels)-1].Query.Where=append(d.Panels[len(d.Panels)-1].Query.Where,"http_route <> ''")
    case "logs":
        if severity!="" {filters=append(filters,"severity = "+literal(severity))}
        add("volume","Log volume","timeseries","logs","count()","count",[]string{"severity"},"auto")
        add("events","Log events","logs","logs","","",nil,"")
        if search!="" {
            d.Panels[len(d.Panels)-1].Options=&panel.Options{Highlight:search}
            d.Panels[0].Query.Where=append(d.Panels[0].Query.Where,"contains(lower(body),lower("+literal(search)+"))")
        }
    case "trace": add("traces","Slow or erroring traces","traces","spans","","",nil,"")
    }
    for i:=range d.Panels {if d.Panels[i].Query.From=="spans" && d.Panels[i].Viz!="health" && d.Panels[i].Viz!="service_map" {d.Panels[i].Drill="traces"}}
    return d
}
```

Use `s.scope` once per preset call, assign `Time{From:&scope.Start,To:&scope.End,Refresh:"off"}` to capture the window (preserves unusual valid duration strings, not only dashboard relative-range enum). Validate existing Limit through `1..500` before clamping preset caps; invalid values must error, not become defaults. Trace input gains `from`/`to` RFC3339Nano fields as a deliberate product-contract extension. Require both together, positive retention-bounded range; the panel query additionally filters exact `trace_id` when specified. For representative selection, obtain Trace detail once and filter the trace-list panel to the selected ID; never show an unrelated list as that trace. Preserve namespace and complete/truncated metadata. No result-shape alias.

Implement one deterministic `fragmentSummary(results []panel.Result) string`. It reports each id, status, row count, truncated flag, diagnosis/error and the first five labelled displayed values/rows in result order; never include SQL, full frame arrays or entire log bodies. UTF-8-safe clipping of labels/cells and a per-panel byte budget keep the whole summary <=16*1024 even with 40 wide panels. Preserve all required panel metadata when reducing sample cell text; tests assert labels and counts, not only byte length.

Implement `boundFragment(fragment PanelFragment) PanelFragment` before summary/return, using a detached payload so truncation cannot mutate executor caches. Marshal to measure <=256*1024; repeatedly select the largest serialized current/previous frame (stable tie by panel id/current-before-previous), drop tail rows with all column values and row-indexed trends together, update Rows, set Truncated and append a visible payload-limit note. Optional trace spans/logs are bounded as row collections too and retain complete/truncated metadata. Re-measure after each cut; never turn payload overflow into a tool failure. Preserve spec/vars/window and diagnostic metadata needed to render the same bounded chart on reload. Keep execution SQL and display diagnostics bounded in the fragment adapter before execution/result assembly; add an adversarial fixed-metadata fixture alongside the large-row tests so the serialized cap holds. Never silently alter authored query semantics or hide the truncation note. No re-query-on-reload strategy.

The named presets always supply explicit query limits: logs 100, traces 50, health/service_map 400; a validated lower caller limit wins. Other panel presets explicitly use 400. Trace detail’s validated input span/log bound remains separate from the trace-list cap. Attach trace detail before boundFragment/fragmentSummary so inspect_trace cannot bypass either cap or return a summary of a different payload. `ToolRegistry.Execute` chooses textContent whenever `r.apps[call.Name]` is nonempty; retains full Structured for the activity. Only non-app tools marshal structured content for the model, and failure there returns an execution error. Remove the fallback warning/import once unused.

- [ ] **Step 3: Consolidate outputs and repair every build consumer**

Replace the Docker two UI stages with **one `ui-build`**, using the existing pinned Bun image: install ui/host lock, copy `ui/*.ts`, `ui/panels`, `ui/host`, run host build, then copy both resulting embedded directories to Go build. Update just ui-deps/lint/audit to one graph, ui-host/build:host and ui-apps/build:apps (recipe names describe outputs, not workspaces), ui depends on both. Move the existing advisory exception to the host audit only if the same locked vulnerable build-only chain remains; record `bun audit` evidence, do not suppress unrelated advisories. Notices collect only `ui/host`. Update lefthook glob, Docker ignore entries and source comments referring to the deleted workspace.

Update `scripts/ui-boundaries.mjs`: `workspace(file)` returns host if first component is host, otherwise shared; remove cross-workspace logic. Preserve parser/type-only/CSS/HTML coverage and forbid shared imports into host or node_modules. Rewrite tests: host→shared and host-local mcp-apps→dashboards allowed; every package import from shared remains rejected. No general ignore for mcp-apps or panels.

Replace `ui-check`'s git restore with staged builds and a byte/path-set comparison. Create `scripts/ui-compare.mjs`:

```js
import { readdirSync,readFileSync } from "node:fs";
import { resolve,relative } from "node:path";
import { fileURLToPath } from "node:url";
export function tree(root) {
  const out=new Map();
  function walk(dir){for(const e of readdirSync(dir,{withFileTypes:true})){const p=resolve(dir,e.name);if(e.isDirectory())walk(p);else if(e.isFile())out.set(relative(root,p),readFileSync(p));else throw new Error("Unexpected non-file asset");}}
  walk(root);return out;
}
export function compare(a,b){const x=tree(resolve(a)),y=tree(resolve(b));return [...new Set([...x.keys(),...y.keys()])].sort().filter(k=>!x.get(k)||!y.get(k)||!x.get(k).equals(y.get(k)));}
if(process.argv[1] && fileURLToPath(import.meta.url)===resolve(process.argv[1])){const errors=compare(process.argv[2],process.argv[3]);if(errors.length){console.error(errors.join("\n"));process.exit(1);}console.log("Embedded assets match.");}
```

Test CLI invocation from root and host. Add a Bun test creating two temp trees, checking identical bytes, changed bytes, extra/removed nested files and symlink rejection; assert input trees are unmodified. Just recipe:

```just
ui-check:
    #!/usr/bin/env bash
    set -euo pipefail
    staged=$(mktemp -d)
    trap 'rm -rf "$staged"' EXIT
    FANOUT_UI_OUT="$staged/ui" FANOUT_APPS_OUT="$staged/apps" just ui
    bun scripts/ui-compare.mjs internal/ui/dist "$staged/ui"
    bun scripts/ui-compare.mjs internal/mcp/apps "$staged/apps"
```

Set final app build output back to `../../internal/mcp/apps`, `emptyOutDir:true`, `publicDir:false`; Task 1a used staging and did not erase old outputs. Delete the ui/apps Dependabot entry, update README/CONTRIBUTING workspace text and the justfile comment about the retired cp targets. `ui-host` invokes only `bun run build:host`, `ui-apps` only `bun run build:apps`; `ui` composes them once each. Add tsc/lint once to the aggregate recipe so the narrower build commands do not skip it.

Extend ui-compare to assert app path set exactly `{panels.html}`, rejecting copied favicon.svg, extra assets and retired HTML, even if both compared trees contain the same extra file. Add a fixture proving that case fails. Keep SPA path/byte comparison generic.

Repair private `.superpowers/replay/build-audit.sh` immediately: in its staged host directory call `bun run build:host -- --mode development --outDir "$stage/dist"` explicitly, then `bun run build:apps` for the generic resource in that staged source. Stage both embeds in the audit binary; use one host lock/node_modules graph. Preserve clean-source precondition and no git writes/restores. Add a shell-command fixture assertion for the explicit build:host invocation and one-app output; controller reviews/commits then builds/runs seeded audit chat checks at this gate. Task 13 does not own this repair.

- [ ] **Step 4: Change AG-UI, resource catalog and bridge security together**

In `Runtime.execute`, mcp-app activity product content becomes `{resource_uri,tool_name,tool_input,tool_result,is_error}`. In `MCPAppContent`, bridge callbacks, ChatMessage, `runtime_test.go`, store fixtures and frame tests change every access to snake_case. Preserve protocol `activityType`, `toolCallId`, `structuredContent`, `resourceUri` in SDK metadata and `threadId`/`runId`; do not snake_case protocol fields. Remove flat `ui/resourceUri` metadata emission/read now as part of the atomic contract change; Task 2's inventory/gate verifies it stays absent. Add a persisted/reloaded activity integration test asserting exact keys and decoded fragment; no old-key fallback. In the agent registry, app tools pass textContent to the model; only non-app structured marshal failure becomes a tool execution error.

Resource registration is a single `panelsAppURI="ui://fanout/panels.html"` pointing at apps/panels.html. Generic view contracts are documented by docgen; update server instructions/tool descriptions and `toolLabels` for `query_telemetry`; do not put the full large specGuide on every tool.

Before stripping UI metadata for non-Apps clients, `filterMCPAppToolMetadata` drops every tool with `visibility:["app"]` from tools/list; ordinary model-visible views retain meaningful summary + structured result. Internal provider Definitions also excludes app-only tools. In MCPAppFrame, fetch/cache negotiated tools/list and forward iframe oncalltool only when that actual tool's nested visibility includes app; reject create/edit/replace/restore, unknown names and model-only tools before mcpClient.callTool. Mark every view the iframe needs (including inspect_trace) model+app visible. Tests cover non-Apps discovery and a forged iframe mutation request, plus cancellation for permitted calls.

Make onCopyLink optional in PanelCard and hide unsaved-fragment Copy link. Chat fragments have agentAvailable=false and no Explain/fix/mutating callbacks in menu, error or modal states. Remove the unimplemented App.openLink usage/capability from this app bridge; saved-dashboard Copy link remains the host URL/clipboard flow. No openLinks advertisement without a handler.

Validate activity content before reading it. A persisted camelCase/pre-M3 activity or retired resource URI renders the existing “This view could not be loaded. Please try again.” Alert without throwing, interpreting old keys or querying a replacement URI. Test through ChatMessage and frame, including missing keys.

Cache raw validated resource text + metadata per URI with a shared in-flight promise (immutable catalog per process; advertised TTL retained). Cache only successful matching MIME/URI reads, evict failures, reapply current CSP on use; tests mount multiple activities/reconnect and assert one readResource for the URI, and no poisoned cache after failure.

- [ ] **Step 5: Green gates, deletion and measured live-chat handoff**

Build first, then delete ui/apps and old embedded outputs via file edits; regenerate notices/docs and verify. Controller verifies all five preset views plus a custom query_telemetry result in chat in light/dark, using actual MCP tool invocation and screenshot/console/network evidence. Include a 61-node fixture for inline worker/CSP, and click→exemplars→trace logs cancellation; no new paid agent request required.

Before bytes from this survey:

| Retired output | Bytes |
|---|---:|
| logs.html | 1,633,982 |
| overview.html | 1,058,218 |
| performance.html | 1,642,622 |
| topology.html | 1,672,174 |
| trace.html | 1,069,822 |
| Total | 7,076,818 |

Record **actual after** panels.html raw bytes and gzip bytes in Task 1b report and the final benchmark document; after values are not knowable before implementation. Target raw panels.html ≤ 2,500,000 bytes and aggregate less than 7,076,818; if exceeded, inspect import graph/fonts/worker duplication and reduce it before acceptance. Keep host MCPAppFrame lazy; app entry should not import the SPA or all routes. Inline single-file output cannot lazy-load an external chunk under this CSP; favor tree-shaken chart registrations over a fake lazy network split. Report SPA route chunks before/after too, without inventing an unmeasured threshold.

```bash
rtk just test ./internal/mcp/... ./internal/agent/... ./internal/cmd/notices/... ./internal/api/... ./cmd/fanout-docgen/...
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk proxy bun test scripts/ui-boundaries.test.ts scripts/ui-compare.test.ts
rtk just ui
rtk just ui-check
rtk just notices
rtk just notices-check
rtk just docs-generate
rtk just docs-generate-check
rtk proxy wc -c internal/mcp/apps/panels.html
rtk proxy sh -c 'gzip -9 -n -c internal/mcp/apps/panels.html | wc -c'
rtk just fmt
```

Controller reviews contract breaks, deletes, generated artifacts and both-theme screenshots before committing. All new code/tests above are implementation work, not results claimed by this plan.

## Task 2: Zero legacy, behavioral test names and CI dead-code guards

**Files:** delete `internal/api/observability.go`, `ui/host/src/observability.ts`, `ui/host/src/echart.tsx`; replace/delete their obsolete tests as listed below. Modify `cmd/fanout/main.go`, `internal/api/auth_middleware.go`, `internal/api/auth_test.go`, `cmd/fanout-docgen/groups.go`, `cmd/fanout-docgen/routes_test.go`, `internal/metrics/metrics.go`, `internal/panel/rollup_panels.go`, `internal/observability/performance.go`, `internal/observability/logs.go`, `internal/observability/contracts.go`, `ui/contracts.ts`, `ui/host/src/rail.tsx`, `ui/host/src/rail.test.tsx`, `ui/host/src/dashboards/api.ts`, `ui/host/src/dashboards/api.test.ts`, `ui/host/src/dashboards/grid.tsx`, `ui/host/src/dashboards/grid-edit.test.tsx`, `ui/host/src/auth-session.ts`, `ui/host/src/auth-session.test.ts`, `ui/host/src/auth.tsx`, `ui/host/src/routes/index.tsx`, `internal/auth/oauth_scope.go`, `internal/auth/oauth_scope_test.go`, `internal/auth/oauth_store_test.go`, `internal/api/oauth_test.go`, `internal/mcp/server.go`, `internal/mcp/server_test.go`, `internal/agent/tools.go`, `.golangci.yml`, `justfile`, `ui/host/package.json`, `ui/host/bun.lock`; create `ui/knip.json`, `scripts/go-deadcode.mjs`, `scripts/go-deadcode.allowlist`, `scripts/dead-code-gates.test.ts`, `internal/api/traces.go`, `internal/api/traces_test.go`, `internal/observability/health_trend.go`, `internal/observability/health_trend_test.go`. Modify `AGENTS.md`, `internal/query/{batch_cache.go,file_snapshot.go,file_snapshot_test.go,views.go,views_test.go}`, `internal/queryrows/window.go`, `internal/id/id_test.go`, `internal/cmd/notices/{main.go,main_test.go}`, generated notices, `go.mod`/`go.sum` (controller pre-step only). Renames/merges and generated docs/embedded files are part of this task.

Budget: $0. Migrations and published checksums are retained unchanged.

### Deletion/replacement inventory

| Delete/retire | Replacement / retained dependency |
|---|---|
| ui/apps package.json/bun.lock/vite.config.ts/tsconfig.json and overview/performance/topology/logs/trace HTML and all ten source entries/CSS | Task 1b host mcp-apps shell + generic panels entry + shared dashboard renderer |
| old five embedded HTML/resources/constants overviewAppURI/topologyAppURI/performanceAppURI/logsAppURI/traceAppURI | one panelsAppURI/output; delete old resource tests and replace with generic negotiated resource tests |
| app-specific Overview/Performance/Topology/Logs Result rendering and dual-axis latency/error correlation | fragment presets, panel specs/results and one-axis panels |
| ObservabilityHandler, ObservabilityQueries, NewObservabilityHandler, observabilityDeadline, bounded/request/overview/topology/performance/logs/dependencies methods and registrations | RegisterPanelRoutes, new RegisterTraceRoutes; MCP Dependencies retains bounded traversal |
| mapQueryError in retired handler | traceError in traces.go with cancellation/deadline/invalid-scope classification |
| ui/host/src/observability.ts exports observabilityParams/observabilityKey/useObservability and its `freshFor` consumer | rail uses GET telemetry/schema via React Query, 60-second schema TTL; dashboard frame reads use queryPanels; dashboards list uses new dashboardsStaleTime=30_000 exported from dashboards/api.ts |
| ui/host/src/echart.tsx and echart.test.tsx (test-only SVG wrapper) | EChartCanvas and existing lifecycle/real-engine tests; retain unique resize/dispose assertions by moving them there |
| HTTP trace query endpoint and API tests/mocks/docs/auth patterns/old metric route label | GET /api/traces/:id, api getTrace uses encoded path ID; absolute window/namespace preserved |
| HTTP services/dependencies endpoint | get_service_dependencies (no new unused HTTP alias) |
| Observability Performance and PerformanceOptions, endpoints/comparison/heatmap/totals payloads and SQL | structured performance fragment; health error trend is the only retained read from its points SQL and gets its own method |
| Observability Logs method and Logs/LogBucket/severity-count payloads and SQL | redacted logs/log_patterns panel executor; correlated trace logs retained in trace.go |
| performance.go once HealthErrorTrend extracted; logs.go; performance_test.go, performance_heatmap_test.go, performance_rollup_test.go | panel engine/compiler/fixture tests; unique exact-boundary/cache equivalence assertions move before deletion |
| rawEndpointsQuery/completedEndpointsQuery/queryEndpoints/endpointDurationBuckets/endpointDurationColumns/histogramQuantile/performanceAggregate/totalsOf/comparisonMetric/latencyNoiseMS | no remaining product caller after Performance removal; delete reader-less endpoint/log cache contributions and their obsolete tests after reader verification; trace cache retains live Trace consumers |
| Performance-related and Logs-related sections of service_test.go, batch_reads_test.go and batch_reads_bench_test.go | preserve Trace candidate/cache/snapshot tests and benchmark; port unique log-redaction and bounded panel assertions to panel fixture tests |
| Go PerformanceSchema/LogsSchema and their payload types in internal/observability/contracts.go; TS Performance/PerformancePoint/PerformanceTotals/LogBucket/Logs and unused Overview/Topology mirrors in ui/contracts.ts | retain Go active Overview/Topology/Dependencies/Trace contracts and TS TraceSpan/TraceDetail/LogEntry/Result plus live panel dependencies; knip decides additional shared exports/files, no blanket ignore |
| ui/resourceUri flat metadata in appToolMeta/filterMCPAppToolMetadata/appResourceURI and old-key tests | Task 1b owns deletion; Task 2 only verifies nested protocol _meta.ui.resourceUri is the sole contract |
| read_endpoints/read_logs writes, endpoint_minutes/endpoint_tail/log_minutes/log_tail sources, queryrows.EndpointRead/LogHistogramRead and their reader-only tests/helpers | no remaining product reader after Performance/Logs removal; delete, bump readCacheVersion 3→4, keep live trace caches and update AGENTS.md |
| legacyMCPScopeRead/legacyMCPScopeDashboard and positive scope-alias tests | only telemetry:read/dashboard:manage; old grants fail validation/re-authentication, no canonicalization shim |
| legacyTokenKey/clearLegacySession and imports/calls/test acceptance; root LegacyChatRedirect and stored fanout.thread-id hop | cookie session clearSession; root navigates to chat if configured, dashboards otherwise |
| react-grid-layout/legacy imports and corresponding mocks | installed v2 Responsive, dragConfig/resizeConfig/verticalCompactor |
| live widget wording in metrics and theme tests, internal/id/id_test.go invalid-id literal; widget-audit filename | panel wording / panel-rendering behavior tests; migration history and third-party license names preserved |

API route labels are built from registered routes, not a static observability metric catalog. Removing registrations retires those labels for new processes; do not rewrite historical external Prometheus series or touch unrelated metrics. Run a literal search over live source and regenerated references; old paths legitimately remain in immutable historical design/plans, the deletion inventory itself and negative retirement tests. The acceptance search excludes those locations and old migration history, and must have no positive live references.

**Controller pre-step before dispatching Task 2:** with network access and the controller’s Go environment, add `tool golang.org/x/tools/cmd/deadcode` to go.mod and pin `golang.org/x/tools` to **v0.50.0**, fetching go.sum/tool modules. Codex keeps GOFLAGS=-mod=readonly and GOPROXY=off and never fetches a substitute. Verify the notices generator’s production-package traversal ignores tool-only modules, or regenerate notices for any changed runtime dependencies; pin changes are reviewed here.

- [ ] **Step 0: Prove knip 6.38.0 on this tree before retirement work**

Controller makes exact `knip@6.38.0` available as a host devDependency with the host lockfile. Its oxc parser has no TS compiler-API dependency; TS 7.0.2 is retained. First write/run temporary-copy failure probes for an unused file and an unused exported helper in each of ui/host/src, ui/panels and ui/*.ts. Entry imports the production module but no test roots it. Run the proposed `ui/knip.json` from ui/ through host’s package script; record its real output and confirm all six planted cases are reported. Baseline findings are recorded for cleanup in Step 5; after cleanup it must report nothing besides planted findings in the probes. Missing pinned tool is BLOCKED, not authority for a newer version or a broad ignore.

- [ ] **Step 1: Write failing retirement, trace and guard tests**

Create `traces_test.go` with the following complete probe/test plus cases for over-retention, missing from/to, wrong method, cancellation and deadline errors. Auth suite already provides viewer sessions; retarget its telemetry-read assertions to `/api/traces/abc`, assert anonymous 401 and authenticated viewer success, and assert unregistered retired endpoints return 404.

```go
package api
import (
 "context"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"
 "github.com/labstack/fanout/internal/observability"
)
type traceProbe struct{scope observability.Scope; id string; bounded bool}
func(p *traceProbe) Trace(ctx context.Context,s observability.Scope,id,service string,limit int)(observability.Result[observability.TraceDetail],error){p.scope=s;p.id=id;_,p.bounded=ctx.Deadline();return observability.Result[observability.TraceDetail]{},nil}
func TestTraceRoutePreservesCapturedNanoseconds(t *testing.T){
 p:=&traceProbe{};s:=newTestAuthServer(t)
 if _,err:=s.users.Create("admin@example.com","","admin");err!=nil {t.Fatal(err)}
 viewer,err:=s.users.Create("viewer@example.com","","viewer");if err!=nil {t.Fatal(err)}
 RegisterTraceRoutes(s.e,p,7)
 rec:=httptest.NewRecorder();s.e.ServeHTTP(rec,sessionRequest(http.MethodGet,"/api/traces/abc?namespace=shop&from=2026-10-01T12:00:00.123456789Z&to=2026-10-01T13:00:00.123456789Z",nil,s.login(t,viewer)))
 if rec.Code!=200||p.id!="abc"||p.scope.Namespace!="shop"||p.scope.Start.Nanosecond()!=123456789||p.scope.End.Sub(p.scope.Start)!=time.Hour||!p.bounded{t.Fatalf("%d %+v",rec.Code,p)}
}
```

Add guard failure tests **before** the gates: an unreferenced exported function in internal/, an exported function used only by a _test.go file, an unused cmd helper, and the six TS file/export probes from Step 0. Run in temporary copies, delete via cleanup, never alter the main tree to inject failures. Expect nonzero gate exit and offending path/symbol; normal tree must pass. Exclude tests as production roots so a test-only function use cannot make dead product code appear live. Do not introduce tests for implementation strings merely to preserve unused exports.

```bash
rtk just test ./internal/api/... -run TestTraceRoutePreservesCapturedNanoseconds
rtk proxy bun test scripts/dead-code-gates.test.ts
```

Expected RED: missing RegisterTraceRoutes/guard commands.

- [ ] **Step 2: Move trace routing and extract the active health read**

Create `traces.go` with this complete route adapter. Exact-ID trace selection remains separate from MCP's representative-service selection. Update classifyRoute only for one nonempty id segment, not a broad wildcard prefix; HEAD follows the registered GET's read semantics. Update main/docgen/auth/API/drill tests together and delete the obsolete handler.

```go
package api
import (
    "context"
    "errors"
    "net/http"
    "strconv"
    "strings"
    "time"
    "github.com/labstack/echo/v5"
    "github.com/labstack/fanout/internal/observability"
)
type TraceQueries interface { Trace(context.Context,observability.Scope,string,string,int)(observability.Result[observability.TraceDetail],error) }
type TraceHandler struct { queries TraceQueries; maxWindow time.Duration }
func RegisterTraceRoutes(e *echo.Echo,queries TraceQueries,retentionDays int) {
    days:=30;if retentionDays>0 {days=retentionDays}
    h:=&TraceHandler{queries:queries,maxWindow:time.Duration(days)*24*time.Hour}
    e.GET("/api/traces/:id",h.get,RequireCapability(ReadTelemetry))
}
func (h *TraceHandler) get(c *echo.Context) error {
    bad:=func()error{return echo.NewHTTPError(http.StatusBadRequest,"invalid trace scope")}
    id:=strings.TrimSpace(c.Param("id"));namespace:=strings.TrimSpace(c.QueryParam("namespace"))
    from,e1:=time.Parse(time.RFC3339Nano,c.QueryParam("from"));to,e2:=time.Parse(time.RFC3339Nano,c.QueryParam("to"))
    if id=="" || len(namespace)>200 || e1!=nil || e2!=nil || !from.Before(to) || to.Sub(from)>h.maxWindow {return bad()}
    limit:=200
    if raw:=c.QueryParam("limit");raw!="" {var err error;limit,err=strconv.Atoi(raw);if err!=nil || limit<1 || limit>500 {return bad()}}
    ctx,cancel:=context.WithTimeout(c.Request().Context(),20*time.Second);defer cancel()
    out,err:=h.queries.Trace(ctx,observability.Scope{Start:from.UTC(),End:to.UTC(),Namespace:namespace},id,"",limit)
    if err!=nil {return traceError(err)}
    return c.JSON(http.StatusOK,out)
}
func traceError(err error) error {
    switch {
    case errors.Is(err,observability.ErrInvalidScope),errors.Is(err,observability.ErrInvalidLimit):return echo.NewHTTPError(http.StatusBadRequest,"invalid trace scope").Wrap(err)
    case errors.Is(err,context.DeadlineExceeded):return echo.NewHTTPError(http.StatusGatewayTimeout,"trace query timed out").Wrap(err)
    case errors.Is(err,context.Canceled):return echo.NewHTTPError(499,"trace query canceled").Wrap(err)
    default:return echo.NewHTTPError(http.StatusInternalServerError,"trace query failed").Wrap(err)
    }
}
```

Replace RollupReader.Performance with new `HealthErrorTrend(context.Context,Scope)([]float64,error)`. Extract only the old points query's time buckets and weighted error_rate expression into `health_trend.go`; use the same scope namespace **and service** predicates, `timelineBucketWidth`, and pinned engine/queryrows window. Return percent values so the frame appends them without an extra multiply. Keep Overview/Topology/Dependencies/Trace and RedactLogBodySQL active. Add executed fixture coverage proving health overview/error trend agrees for scoped services, an empty scope does not report healthy, and no endpoints/heatmap/comparison queries run. Delete Performance/Logs SQL only after no live caller remains. Keep latency_sql.go (Overview/Topology use it), trace selection/cache/redaction helpers and every cache with a verified live reader. Delete the reader-less contributions below.


**Reader-less cache removal (I3):** Before deleting, run `rg -n 'EndpointRead|LogHistogramRead|endpoint_minutes|endpoint_tail|log_minutes|log_tail|read_endpoints|read_logs' internal cmd ui scripts` excluding generated/minified/vendor files, and classify every hit. This survey found the product reads only in observability/performance.go and logs.go; re-check intelligence, alerts, MCP and panels at dispatch. If an additional live reader appears, keep that specific cache and report caller/reason rather than orphaning it.

After the last readers are removed, delete endpoint/log schema DDL (`createReadEndpointTable` in views.go and read_logs DDL), inserts/deletes/compaction-transfer SQL in batch_cache.go, endpoint/log aggregateSources branches, four snapshot relation names in file_snapshot.go, the two queryrows read kinds, and now-unused minuteInterior/timeNanosLiteral/bin helpers. Remove their dedicated tests in file_snapshot_test.go/views_test.go/observability tests; port unique snapshot pinning, schema rebuild, write-gate, publication, row-budget, compaction and retention assertions to remaining trace-candidate fixtures first. Set `readCacheVersion=4`: existing version-3 private read tables all rebuild from immutable Parquet, including dropping obsolete endpoint/log tables; no legacy schema branch. Keep read_batches/read_trace_parts/read_trace_candidates and the version/service/edge/annotation caches with live readers.

Update AGENTS.md in this same task: its acknowledgment sentence now names notable-trace candidates; replace removed minute-histogram semantics with per-batch trace bounds and one incremental candidate per trace. Keep transactional complete-batch IDs, pinned marker/row read, active uncached visibility, exact scoped Parquet reads, mixed trace batch parts, compaction/retention balance, schema/semantics versioning, disjoint gates/pool slots and maintenance checkpoint rules. Engine tests seed version 3 with obsolete read tables, rebuild to 4, assert retired tables absent, then ingest→compaction→retention (including late publication/uncached replacement/mixed scope) and verify remaining trace candidate counts/bounds and version markers without double-count/loss.

Complete `health_trend.go` (no endpoint/cache alternative):

```go
package observability
import (
    "context"
    "fmt"
    "time"
    "github.com/labstack/fanout/internal/queryrows"
)
func (s *Service) HealthErrorTrend(ctx context.Context,scope Scope)([]float64,error) {
    scope,err:=s.normalizeScope(scope);if err!=nil {return nil,err}
    ctx=queryrows.WithWindow(ctx,queryrows.Window{Start:scope.Start,End:scope.End,Namespace:scope.Namespace,Service:scope.Service})
    sql:=fmt.Sprintf(`SELECT time_bucket(INTERVAL '%s',bucket) AS point_time,
      100.0*COALESCE(SUM(error_rate*spans)/NULLIF(SUM(spans),0),0)
      FROM service_rollup WHERE bucket>=? AND bucket<?
      AND (?='' OR namespace=?) AND (?='' OR service=?)
      GROUP BY point_time ORDER BY point_time`,timelineBucketWidth(scope.End.Sub(scope.Start)))
    rows,err:=s.db.QueryContext(ctx,sql,scope.Start,scope.End,scope.Namespace,scope.Namespace,scope.Service,scope.Service)
    if err!=nil {return nil,fmt.Errorf("query health trend: %w",err)}
    defer rows.Close();out:=[]float64{}
    for rows.Next(){var at time.Time;var rate float64;if err:=rows.Scan(&at,&rate);err!=nil{return nil,fmt.Errorf("scan health trend: %w",err)};out=append(out,rate)}
    if err:=rows.Err();err!=nil{return nil,fmt.Errorf("iterate health trend: %w",err)}
    return out,nil
}
```

HealthErrorTrend replaces the old points read only; its rollup bucket predicates preserve existing rollup semantics. Raw telemetry predicates elsewhere keep native TIMESTAMPTZ_NS and explicit parameter casts. Trace/cache engine fixture correctness is preserved before Performance tests are retired.

Define `dashboardsStaleTime=30_000` beside dashboardsKey in dashboards/api.ts; rail’s dashboards query imports it instead of freshFor. Rail service search becomes `useQuery({queryKey:["telemetry-schema","1h"],queryFn:...GET /api/telemetry/schema?window=1h,enabled:Boolean(query)&&Boolean(onInvestigateService),staleTime:60_000})`; map real `schema.services` fields, not the former overview shape. Verify schema fields from `internal/panel/schema.go` when editing; `services` is structured, not a guessed string list. Include error/empty/service match tests and preserve search debouncing.

- [ ] **Step 3: Remove aliases, retire the grid legacy adapter, review all markers**

For v2 grid use the installed declarations, changing imports/mocks together:

```tsx
import { Responsive, verticalCompactor, type Layout } from "react-grid-layout";
// On the existing Responsive element, replace the old drag/resize props with:
// compactor={verticalCompactor}
// dragConfig={{enabled:canEdit,handle:".panel-drag",cancel:"button"}}
// resizeConfig={{enabled:canEdit}}
```

Keep rowHeight/margin/cols/breakpoints/layouts/width and onDragStop/onResizeStop; Layout is readonly in v2, so copy at state boundaries. Never use onLayoutChange to persist mount/resize compaction. Existing grid-edit tests must still prove only user drag/resize saves coordinates, unchanged panels' authored widths survive, duplicate/remove respect dirty layout, and mobile layout is not saved. Controller verifies drag/resize at both widths in final collector.

**Marker ledger** (all source occurrences found in the survey, grouped by behavior; no requirement to erase the word from third-party licenses or rejection tests):

| Location / marker | Action and one-line reason |
|---|---|
| internal/auth/oauth_scope.go, oauth_scope_test.go, oauth_store_test.go; internal/api/oauth_test.go legacy canonicalization | Remove aliases; replace positive tests with rejection of retired names and retain canonical grant narrowing. |
| internal/mcp/server.go / server_test.go deprecated flat UI metadata; internal/agent/tools.go old-key reader | Task 1b deleted flat compatibility metadata/reader; only verify its absence here, with no duplicate implementation ownership. |
| ui/host/src/auth-session.ts / auth.tsx / auth-session.test.ts legacy local-storage token cleanup | Delete unused migration cleanup; cookie sessions own authentication. |
| ui/host/src/routes/index.tsx LegacyChatRedirect | Remove old stored-thread redirect; retain deliberate root navigation based on configured chat. |
| ui/host/src/rail.test.tsx “Legacy migration” fixture | Rename to an old historical chat fixture; its pagination/age test is active and not a compatibility path. |
| ui/host/src/dashboards/grid.tsx / grid-edit.test.tsx /legacy import | Move to v2 API; compatibility package is no longer a product dependency. |
| internal/mcp/server.go text/structured “fallbacks”, event-stream “405 fallback” | Retain text+structured for non-Apps clients and stateful protocol stream; this is capability negotiation/runtime recovery, not a retired contract. |
| internal/dashboard/identity.go, internal/mcp/dashboards.go, internal/mcp/server_test.go _meta fallback | Retain authenticated in-process owner injection; remote OAuth TokenInfo always takes precedence and spoofing rejection tests stay. |
| internal/agent/tools.go JSON marshal-to-text fallback | Task 1b owns removal: app tools use deliberate text summaries; non-app marshal failure errors. Task 2 verifies only. |
| internal/api/auth_middleware.go metrics admin-session fallback | Retain admin read access when operational token is unavailable; existing explicit permission policy remains. |
| internal/api/oauth.go and oauth_test.go IPv6 scheme-source fallback | Retain conservative CSP scheme source for valid IPv6 callback URLs; it is a browser CSP limitation, not an old API. |
| internal/config/sizing.go cgroup_fallback and config_test.go documented sizing fallback | Retain container resource detection when procfs metadata is unavailable; measured memory limits must still work. |
| internal/query/duck.go writer() test-constructed write-pool fallback | Remove `return d.DB`; NewDuck always sets writeDB, and field-by-field write fixtures must explicitly set writeDB to their test handle. Preserve safe Close for partial construction/initialization failures. |
| internal/ingest/server_test.go fallback attribute keys | Retain ordered OTLP attribute lookup; the second key is real telemetry semantics. |
| internal/ingest/template_test.go invalid JSON fallback to text | Retain treating unparseable log bodies as text; log bodies are legitimately arbitrary text. |
| internal/ingest/http_test.go, auth_test.go rejected legacy header; internal/api/auth_test.go no legacy access token | Retain negative tests; they enforce absence of retired credentials rather than support them. |
| internal/api/oidc_test.go rejected fallback email values | Retain rejecting missing/untrusted email claims; no invented identity. |
| internal/telemetry/store/repository_test.go no fallback marker | Retain forbidden recovery marker test; operational failures cannot authorize schema/format repair. |
| internal/ui/ui.go / ui_test.go SPA fallback | Retain serving the SPA for registered client routes, with missing assets/API paths failing closed. |
| internal/panel/frame.go dynamicType fallback | Retain result type inference for null/dynamic cells; declared JSON is preserved. |
| internal/panel/measure.go suggestOr fallback | Retain diagnostic suggestion when no near spelling exists; no parser compatibility branch. |
| internal/panel/spec.go Top fallback | Retain current default row/series limit; omission is a valid v1 input. |
| internal/panel/unit_regression_test.go inferred-unit fallback | Retain unknown-unit handling without mislabeling data; it is render inference. |
| ui/host/src/dashboards/native-audit.test.ts native text-measure fallback | Retain deterministic test-only text metrics; production uses real canvas metrics. |
| ui/host/src/dashboards/new-viz.test.tsx safe fallback cells | Retain sanitized representation for null/unsupported cells; no legacy schema decoder. |
| ui/host/src/chat.tsx Suspense fallback | Retain loading state while lazy app frame loads; no alternate renderer. |
| internal/intelligence/error_rate_sql_test.go CASE zero-rate fallback | Retain arithmetic zero handling; division by absent traffic cannot create infinities. |
| site/src/content.config.ts deprecated Zod re-export comment | Remove outdated explanation once direct Zod import is confirmed; no alternate Astro API retained. |
| site/src/components/Head.astro site fallback | Remove unreachable fallback when required site config is present; test required config instead. |
| justfile Typst fallback face rejection | Retain failing the render when the requested font is absent; silently using a font would invalidate the committed image. |
| copied-source “consolidate in M3” comments in dash trace helpers and echart.tsx | Delete obsolete copy comments/wrapper; shared trace components remain the sole implementation. |
| third-party `character-entities-legacy`, Parquet license text and migrations | Keep immutable/legal names/history; they are not live product compatibility code. |

Repeat the marker search at execution time and append any newly surfaced source location with action/reason to Task 2 report. No unclassified “TODO remove” is accepted. Do not delete genuine runtime fallback coverage to hit a textual zero. Zero applies to **removed symbols/routes/positive compatibility readers**.

- [ ] **Step 4: Rename/merge round-named tests by behavior without losing unique coverage**

| Existing | Destination |
|---|---|
| final2-map.test.tsx | merge into service-map.test.tsx (worker, cancellation, structure cache, scroll, zero-error metrics) |
| final3-cleanup.test.ts | merge active production instrumentation guards into service-map.test.tsx; remove only assertions that duplicate exactly the same source/expected predicate |
| final5-echarts.test.tsx | echarts-brush.test.tsx (real registered BrushComponent) |
| final2-panels.test.ts | panel-overlays.test.ts |
| preview-grade.test.tsx | headline-panels.test.tsx |
| preview-grade10.test.tsx | inspect-views.test.tsx |
| preview-part2.test.ts | chart-units.test.ts |
| preview-part3.test.ts | timeseries-labels.test.ts |
| preview-part4.test.ts | service-map-layout.test.ts |
| preview-part6.test.ts | heatmap-density.test.ts |
| preview-part8.test.tsx | panel-chrome.test.tsx; move health-ranking cases into rollup-panels.test.tsx |
| preview-part10.test.ts | heatmap-scale.test.ts |
| preview-timeseries.test.ts | timeseries-end-labels.test.ts |
| preview-rows.test.tsx | row-formatting.test.tsx |
| widget-audit.test.tsx | panel-rendering.test.tsx |
| m2-acceptance.test.ts | browser-evidence.test.ts |
| native-audit.ts / native-audit.test.ts | chart-measurements.ts / chart-measurements.test.ts |
| chart-audit-dev.ts | chart-measurements-dev.ts (DEV-only collector hook) |

Keep DEV-only measured chart/worker hooks where the controller's collector actually uses them. Their imports must tree-shake from production; assert production contains neither registry access nor data instrumentation (use existing source/compiled-output tests, not test-owned duplicate product exports). If a helper is controller-only and no product/test import remains, move it under `.superpowers/replay`, adjust private collector imports, and exclude it from product build. Do not move package imports into ui/panels. Remove test titles' round identifiers; name the behavior.

Capture Vitest JSON before/after under `.superpowers/`, map every original test name to its retained destination, and record each exact duplicate deleted with both original assertion locations. This plan authorizes **no preselected coverage deletion**; the default is keep every unique assertion. Test count may only fall by the counted exact duplicates; renamed/merged suites must pass independently (hoisted mocks from former files may need isolated describes, not module-global conflicts).

Also rename Go test files and functions by behavior (paths relative to internal/):

| Existing | Destination |
|---|---|
| agent/final_fix_test.go | agent/run_deadline_test.go |
| dashboard/final_fix_test.go | dashboard/write_constraints_test.go |
| mcp/final_fix_test.go | mcp/tool_errors_test.go |
| panel/final_fix_test.go | panel/execution_limits_test.go |
| dashboard/round2_test.go | dashboard/layout_constraints_test.go |
| db/round2_test.go | db/dashboard_query_bounds_test.go |
| mcp/round2_test.go | mcp/dashboard_scope_docs_test.go |
| panel/round2_test.go | panel/frame_semantics_test.go |
| api/exemplars_fix_test.go | api/panel_request_errors_test.go |
| panel/exemplars_fix_test.go | panel/exemplar_bounds_test.go |
| panel/log_source_fix_test.go | panel/log_redaction_test.go |
| panel/table_fix_test.go | panel/table_semantics_test.go |
| agent/provider_fix_test.go | agent/provider_contract_test.go |
| query/annotations_fix_test.go | query/version_gate_test.go for gate-budget cases; query/annotation_maintenance_test.go for remaining version/anomaly maintenance cases |

Rename functions `TestFinalFixX`→`TestX`, `TestRound2X`→`TestX`, `TestM2FixX`→`TestX`, `TestM2TableFixX`→`TestX` and remaining milestone/round prefixes into behavioral names. Specifically `TestFinalFixVersionBudgetStartsAfterGate`→`TestVersionRollupBudgetStartsAfterGate`; Task 11 extends version_gate_test.go. If a behavior-named symbol already exists, merge its exact duplicate or choose a more precise unique name, with accounting. Existing descriptive provider tests retain their names.

Before/after capture test inventories with `rtk just test ./internal/agent/... ./internal/dashboard/... ./internal/db/... ./internal/mcp/... ./internal/panel/... ./internal/api/... ./internal/query/... -list .` (the recipe invokes Go’s test-list mode under the engine wrapper). Map every original Go test to destination/name; count drops only for individually recorded exact duplicates, including both original locations. Run the owning suites after renames. Preserve unique engine-executed assertions before deleting cache-reader suites; those retirement deletions have explicit replacements in Step 2 rather than pretending to be duplicate test cleanup.

- [ ] **Step 5: Install actual dead-code gates, prove failure, then pass**

Pin exactly **knip 6.38.0** as host devDependency and in the single Bun lockfile. Use `ui/knip.json` with ui/ as root so package-free shared modules are inside the analyzed tree. Production entry/project patterns carry trailing `!`; tests are excluded as roots/projects, and production mode cannot silently analyze an empty tree. Initial configuration:

```json
{
  "$schema": "https://unpkg.com/knip@6.38.0/schema.json",
  "entry": ["host/src/main.tsx!", "host/src/mcp-apps/main.tsx!", "host/src/routeTree.gen.ts!"],
  "project": ["host/src/**/*.{ts,tsx}!", "*.ts!", "panels/**/*.ts!", "!host/src/**/*.test.{ts,tsx}!", "!panels/**/*.test.ts!"],
  "vite": {"config": ["host/vite.config.ts", "host/vite.apps.config.ts"]}
}
```

Let Vite’s plugin track the genuine worker import; if it needs an explicit root use `host/src/dashboards/viz/service-map.worker.ts!`, never root all source files to suppress exports. Confirm plugin/config discovery in Step 0 using actual output. Add host script `"deadcode":"cd .. && bun host/node_modules/knip/bin/knip.js --production --config knip.json --include files,exports"`; `ui-deadcode` runs `cd ui/host && bun run deadcode`. No bunx flags, TS compiler API substitute, broad export/dependency ignore or baseline suppression. Normal post-cleanup output is empty, and all planted file/export cases fail.

Keep golangci `unused` module-wide, remove its cmd/ exclusion and enable it. Add `go-deadcode` to check alongside ui-deadcode/lint. Its runner invokes `bash scripts/with-duckdb.sh go tool deadcode -test=false -json ./cmd/...` using the controller-installed **v0.50.0** tool directive, parses all findings, then exits nonzero for every finding absent from `scripts/go-deadcode.allowlist`. Also print/record the unfiltered findings. Include every cmd/ main; tests must not be roots. The reviewed allowlist has exact symbol plus one-line reason only for genuine non-main entry points; delete unused generated sqlc queries and regenerate instead of default-allowing generated methods. No package wildcard or existing-findings baseline. Reject stale/unused allowlist entries.

Inject an exported internal function referenced only from a test and a wholly unused exported internal function in temporary copies; both must make go-deadcode fail with path/symbol. Verify the normal tree passes after deleting findings or controller-reviewing genuine allowlist entries, and record every finding/removal/reason. notices traversal uses production package dependencies; add a fixture proving a tool-only module stays out, or regenerate any changed production notices.

Exact recipes (new runner parses pinned deadcode JSON and reviewed allowlist as specified above):

```just
ui-deadcode:
    cd ui/host && bun run deadcode

go-deadcode:
    node scripts/go-deadcode.mjs
```

Runner child command: `bash scripts/with-duckdb.sh go tool deadcode -test=false -json ./cmd/...`; `check` depends on both recipes and existing module-wide lint. Do not rely on the tool process exit alone: unreachable-symbol findings must make the runner fail. Gate tests assert all cmd main roots were included and test=false was used, then prove reachability behavior with the planted exported/test-only cases. The allowlist file contains symbol<TAB>one-line reason; every retained entry requires controller review, never an automatic baseline.

Run guards, delete real unused code they reveal, then rerun their owning behavioral suites. Never suppress all unreachable exported code simply because existing tests imported it. Additional deletes get exact file/export→replacement-or-no-caller rows in the report. Update internal/cmd/notices and generated inventory for knip (build-time dependency).

```bash
rtk proxy bun test scripts/dead-code-gates.test.ts
rtk just ui-deadcode
rtk just go-deadcode
rtk just lint
rtk just test ./internal/api/... ./internal/auth/... ./internal/mcp/... ./internal/agent/... ./internal/panel/... ./internal/observability/... ./internal/query/... ./cmd/fanout-docgen/... ./internal/cmd/notices/...
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
rtk just notices
rtk just docs-generate
rtk just fmt
rtk just check
```

Retirement search (exit 1 from rg means success/no matches; report exit and output, do not mask failures with `|| true`):

```bash
rg -n '/api/observability/|NewObservabilityHandler|ObservabilityHandler|observabilityParams|observabilityKey|useObservability|legacyMCPScope|clearLegacySession|LegacyChatRedirect|ui/resourceUri|react-grid-layout/legacy' internal cmd ui scripts site/src/content/docs/reference .github README.md CONTRIBUTING.md justfile --glob '!internal/db/migrations/**' --glob '!internal/ui/dist/**' --glob '!**/node_modules/**' --glob '!**/*_test.go' --glob '!**/*.test.*'
rg -n 'widget' internal ui/host/src --glob '!internal/db/migrations/**' --glob '!internal/ui/dist/**'
```

Change internal/id/id_test.go’s invalid value "widget" to "invalid-id"; it still tests the same invalid UUID behavior. Search live workspace references with `rg -n 'ui/apps' .github README.md CONTRIBUTING.md justfile Dockerfile .dockerignore lefthook.yml scripts internal/cmd/notices` and require no positive references. Additionally inspect compiled HTML/SPA for retired resource URIs/payload keys and forbidden external assets using scripts, rather than printing giant minified lines. New negative tests can spell retired contracts intentionally; they must assert rejection, not alias acceptance. If sandbox module cache prevents golangci-lint, state the exact failure and hand that one gate to the controller; no PASS until its real output exists.

## Task 3: Promote the eval runner and prove its scoring without model spend

**Files**
- Create: `scripts/dashboard-eval/main.ts`, `scripts/dashboard-eval/score.ts`, `scripts/dashboard-eval/score.test.ts`, `scripts/dashboard-eval/transport.ts`, `scripts/dashboard-eval/transport.test.ts`, `scripts/dashboard-eval/mock.ts`, `scripts/dashboard-eval/README.md`.
- Read, do not check in: `.superpowers/eval/eval2.ts`, `.superpowers/eval/mock2.ts`, `.superpowers/eval/README.md`; **do not open the holdout prompt file**.
- Modify: `justfile` (mock runner in checks), `internal/agent/runtime.go` only if correlated per-call provider usage records are missing; Create `internal/agent/usage_ledger_test.go` for metering. Eval CLI is outside the ui/ knip project; no gate-root edit required.

Budget: **$0**. Mock transport tests buy reliable scoring and retry/cost behavior before a paid run. The first full paid baseline is Task 12, after the receipt contract exists; paying to measure a knowingly missing save-check contract now would not help.

- [ ] **Step 1: Specify strict S1–S5 scoring and write the failing tests**

Create this complete core in `score.ts` after its test fails. The run adapter supplies immutable save observations, not the latest dashboard read; each check covers all panel IDs including text panels. Empty states require both an executor diagnosis and an authored description explaining absence. Preserve all record fields in edit comparisons; normalize object key order only. Grid changes are allowed only when an operation actually changes placement, and record them separately even then.

```ts
export type SavedPanel = { id:string; description?:string };
export type Check = { id:string; status:string; rows:number; diagnosis?:string };
export type Run = {
  saved:boolean; elapsed_ms:number|null; valid:boolean; checked:boolean;
  panels:SavedPanel[]; checks:Check[];
};
export type Edit = { passed:boolean; changed_ids:string[]; expected_ids:string[]; layout_changed?:boolean };
export function median(xs:number[]):number|null {
  if (!xs.length) return null;
  const sorted=[...xs].sort((a,b)=>a-b), i=Math.floor(sorted.length/2);
  return sorted.length%2 ? sorted[i] : (sorted[i-1]+sorted[i])/2;
}
export function panelPass(p:SavedPanel, checks:Check[]):boolean {
  const matches=checks.filter(c=>c.id===p.id);
  if(matches.length!==1)return false;
  const c=matches[0];
  return c.status==='ok' && c.rows>0 || c.status==='empty' && Boolean(c.diagnosis?.trim()) && Boolean(p.description?.trim());
}
export function score(runs:Run[], edits:Edit[]) {
  const complete=runs.length===10;
  const times=runs.map(r=>r.elapsed_ms);
  const latency=times.every(t=>t!==null && Number.isFinite(t) && t>=0) ? median(times as number[]) : null;
  const total=runs.reduce((n,r)=>n+r.panels.length,0);
  const good=runs.reduce((n,r)=>n+(r.checked ? r.panels.filter(p=>panelPass(p,r.checks)).length : 0),0);
  const validation_failures=runs.filter(r=>r.saved && !r.valid).length;
  const s1=complete && runs.every(r=>r.saved);
  const s2=complete && total>0 && good===total && runs.every(r=>r.checked && r.panels.length>0 && r.checks.length===r.panels.length);
  const s3=complete && runs.every(r=>r.saved) && validation_failures===0;
  const s4=complete && latency!==null && latency<=45000;
  const s5=edits.length===5 && edits.every(e=>e.passed && stable([...e.changed_ids].sort())===stable([...e.expected_ids].sort()));
  return {s1,s2,s3,s4,s5,passed:s1&&s2&&s3&&s4&&s5,saved:runs.filter(r=>r.saved).length,total,good,validation_failures,median_ms:latency};
}
export function stable(value:unknown):string {
  const sort=(v:unknown):unknown => Array.isArray(v)?v.map(sort):v!==null && typeof v==='object' ? Object.fromEntries(Object.entries(v).sort(([a],[b])=>a.localeCompare(b)).map(([k,x])=>[k,sort(x)])) : v;
  return JSON.stringify(sort(value));
}
export function changedPanels(before:{id:string}[],after:{id:string}[],allowGrid:boolean):string[] {
  const strip=(p:{id:string})=>allowGrid ? Object.fromEntries(Object.entries(p).filter(([key])=>key!=='grid')) : p;
  const a=new Map(before.map(p=>[p.id,stable(strip(p))])),b=new Map(after.map(p=>[p.id,stable(strip(p))]));
  return [...new Set([...a.keys(),...b.keys()])].filter(id=>a.get(id)!==b.get(id)).sort();
}
```

```ts
import { expect,it } from 'bun:test';
import { score,changedPanels,type Run } from './score';
const run=():Run=>({saved:true,elapsed_ms:45000,valid:true,checked:true,panels:[{id:'latency'}],checks:[{id:'latency',status:'ok',rows:2}]});
const edits=()=>Array.from({length:5},()=>({passed:true,changed_ids:['latency'],expected_ids:['latency']}));
it('requires all ten saves and all five consecutive edits',()=>{
  expect(score(Array.from({length:10},run),edits()).passed).toBe(true);
  expect(score(Array.from({length:9},run),edits()).s1).toBe(false);
  expect(score(Array.from({length:10},run),edits().slice(1)).s5).toBe(false);
});
it('never accepts unchecked, unexplained or duplicate panel evidence',()=>{
  for(const change of [ (r:Run)=>{r.checked=false},(r:Run)=>{r.checks[0].status='empty'},(r:Run)=>{r.checks.push({...r.checks[0]})} ]) {
    const runs=Array.from({length:10},run);change(runs[0]);expect(score(runs,edits()).s2).toBe(false);
  }
  const runs=Array.from({length:10},run);runs[0].checks[0]={id:'latency',status:'empty',rows:0,diagnosis:'No matching spans'};
  runs[0].panels[0].description='This service has no spans in the selected period.';
  expect(score(runs,edits()).s2).toBe(true);
});
it('fails missing latency and measures a true even-sized median',()=>{
  const runs=Array.from({length:10},run);runs[0].elapsed_ms=null;expect(score(runs,edits()).s4).toBe(false);
  runs.forEach((r,i)=>r.elapsed_ms=i<5?40000:50000);expect(score(runs,edits()).median_ms).toBe(45000);
});
it('detects unrelated changes, deletion, addition and reordering in field arrays',()=>{
  const a=[{id:'a',query:{by:['service','namespace']}},{id:'b',title:'B'}];
  expect(changedPanels(a,[{...a[0],query:{by:['namespace','service']}},a[1]],false)).toEqual(['a']);
  expect(changedPanels(a,[{...a[0]},{id:'c'}],false)).toEqual(['b','c']);
});
```

The S5 adapter must additionally compare dashboard metadata, time, annotations and variables, panel order, exact expected field changes, and grids. A no-op named edit fails: `changed_ids` must equal expected IDs and the requested result must exist. Permit packed grids only for add/remove/move; title/unit/threshold edits preserve every unrelated byte after canonical JSON ordering. Do not let the generic `allowGrid` flag hide unrelated layout mutations on ordinary edits. Scorer records layout_changed once by comparing physical grid/order separately; the changedPanels helper strips only physical grid when placement is explicitly allowed, and never width/height or other authored fields. Test packed-neighbor changes after remove as one layout event with no neighbor authored chip, and reject those same grid changes on a title edit.

- [ ] **Step 2: Promote transport and CLI, leaving prompts/data private**

Reuse the **behavior**, not an unreviewed wholesale copy, of existing `cookieHeader`, `streamState`, `runAgent`, `findSaved`, `executePanels` and `safeOutput`. Implement `transport.ts` exports `readSSE(response,signal)`, `requestJSON(origin,path,init)`, `cookieHeader(jar,url)` and `canStartPrompt(ledger,capUSD)`. SSE parsing must handle split UTF-8/multiline frames, tool-call IDs and assistant call names, terminal RUN_FINISHED/RUN_ERROR, truncation and socket failure. Never retry an agent POST, restore or mutation; one bounded retry is allowed only for an idempotent GET before any response is consumed. A disconnect records an incomplete run and checks persisted run status before any manual controller decision to retry; the CLI itself never repeats it.

`main.ts` supports exactly `--help`, `--mock`, `--base`, `--cookies`, `--model-label`, `--out`, `--prompts-file`, `--set`, `--budget-usd`, `--cost-ledger`, `--snapshot-manifest`, `--edits-file`, `--holdout-sha`. Inputs are private paths provided by the controller; benchmark output must be under `.superpowers/eval/`; holdout output must instead use controller-supplied `FANOUT_HOLDOUT_OUT_ROOT` outside `.superpowers/eval` and the worktree, unavailable to subsequent Codex dispatches. Neither path may traverse symlinks or overwrite an existing run. Holdout files contain prompt ids/hashes only, never prompt text, rendered responses, tool-input text or individual failure content that could reveal the prompt. Reject embedded origin credentials and unsafe output labels. Cookie secrets never enter reports, stdout or committed fixtures. Output schema 3 includes run timestamps, model label AND observed provider/model configuration, candidate source hash, snapshot manifest hash/bounds, saved dashboard/version/checks, expected/actual edit diffs, S1–S5, terminal events, and actual cost. No telemetry rows in the public summary.

The running ledger meters **every model call** from provider-response TokenUsage (input/output/cache read/write; reasoning is a subset of output) and controller-supplied verified provider rates. Runtime already logs per-step usage; require correlation by run_id/step/provider/model and capture incomplete/error calls’ reported usage too. Never log provider response bodies. Add a fake-provider runtime test proving each call’s authoritative usage is recorded once, not inferred from tool text or a typed model label.

Before each create, edit, Explain, targeted rerun or holdout prompt, `canStartPrompt` checks `spent + estimate <= taskCap`, where estimate is measured p95 total cost per completed prompt so far (nearest-rank p95), or **$0.60 before any measurement**. An insufficient balance stops before HTTP. Debit provider-reported call costs immediately during the prompt and settle its measured prompt total before another prompt begins; preserve the runtime’s **16-step** limit. Missing/incomplete usage or an unsettled prompt stops subsequent prompts until the controller reconciles it. This is the controller’s practical spend gate, not a claim that p95 bounds the theoretical cost of an in-flight call. No new product clock/token override. The task cap is the whole ledger across invocations, including optional paid Explain/holdout; mock dollars have no credentials or provider calls.

Use ten private benchmark prompts from the spec. Five edits run consecutively on the same saved dashboard: change one named title; change one named threshold; add one named panel; remove that newly added panel; change one named unit/format. Build private edit prompts/expected diffs from the actual IDs saved in that run, before submitting each turn. Request exact fields, avoid accidental semantic incompatibility such as changing a percentile to count. Do not hard-code demo services or prompt-specific recipes in product code.

Add complete transport tests with a local `Bun.serve` mock: split SSE, one save followed by a later unrelated read, duplicate tool IDs, is-error mutation, missing terminal, truncation, GET 503 retry once, POST 503 no retry, abort, cookie expiry/domain/path, output collision/symlink, insufficient budget before HTTP, exact $0.60 prior, p95 after measurements, equality at cap, accumulation across invocations, per-call usage on failure and incomplete settlement. Add scorer test proving an unsaved timeout is S1 failure with validation_failures=0, while a saved invalid spec counts toward S3; incomplete validation never passes S3. `mock.ts` runs ten simulated saves and five edits through HTTP and the real scorer, then injects each failure and verifies exit nonzero. Add a CLI main guard so imports do not start a server.

```bash
rtk proxy bun test scripts/dashboard-eval
rtk proxy bun scripts/dashboard-eval/main.ts --mock
rtk proxy bun scripts/dashboard-eval/main.ts --help
```

- [ ] **Step 3: Document and hand off**

README documents environment-supplied paths, sealed holdout policy, per-call metering/p95 gate, cost source, scoring, absolute observation time, safe cleanup of disposable owned dashboards, and exit codes: 0 complete pass, 1 measured failure, 2 invalid/incomplete/budget-blocked. Do not delete a controller's real dashboards. Add a mock recipe to `just check`; no paid/network-dependent check in CI. Task report records test evidence and confirms no holdout file was opened. The CLI is ready for Task 12 once Tasks 5–6 supply authoritative checks.

## Task 4: Suppress intermediate narration at the runtime boundary

**Files**
- Modify: `internal/agent/runtime.go`, `internal/agent/runtime_test.go`, `internal/agent/store_test.go`.
- Create: `internal/agent/narration_test.go`; Modify `ui/host/src/chat.tsx`, `ui/host/src/chat.test.tsx` for the still-running indicator.

Budget: $0. This is a stream/persistence correctness change, independent of prompt wording.

- [ ] **Step 1: Reproduce the stray text in a complete failing test**

```go
package agent

import (
    "context"
    "strings"
    "testing"
    agtypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
)

func TestRuntimeKeepsToolNarrationOutOfTranscript(t *testing.T) {
    p:=&scriptedProvider{steps:[][]StreamEvent{
        {{Type:EventText,Delta:"Fixing the sort."},{Type:EventToolUse,ToolCall:&ToolCall{ID:"call-1",Name:"edit_dashboard",Input:`{"id":"board","operations":[]}`}},{Type:EventStop,StopReason:"tool_use"}},
        {{Type:EventText,Delta:"Updated the latency panel."},{Type:EventStop,StopReason:"end_turn"}},
    }}
    tools:=&fakeTools{execution:ToolExecution{Content:`{"dashboard":{"id":"board","version":2}}`}}
    r:=NewRuntime(p,tools,nil)
    emitter,out:=newTestEmitter()
    messages:=[]agtypes.Message{{ID:"user",Role:agtypes.RoleUser,Content:"Sort latency worst first"}}
    if _,err:=r.execute(context.Background(),"thread","run",&messages,emitter);err!=nil {t.Fatal(err)}
    if strings.Contains(out.String(),"Fixing the sort.") {t.Fatal("intermediate text escaped on SSE")}
    if !strings.Contains(out.String(),"Updated the latency panel.") {t.Fatal("final answer missing")}
    for _,m:=range messages {if m.Role==agtypes.RoleAssistant && len(m.ToolCalls)>0 && messageText(m.Content)!="" {t.Fatalf("persisted narration: %+v",m)}}
    seen:=false
    for _,m:=range p.got[1] {if m.Role==RoleAssistant && m.Content=="Fixing the sort." && len(m.ToolCalls)==1 {seen=true}}
    if !seen {t.Fatal("provider continuation lost tool-bearing text")}
    assertEventOrder(t,out.String(),"RUN_STARTED","TOOL_CALL_START","TOOL_CALL_END","TOOL_CALL_RESULT","TEXT_MESSAGE_START","TEXT_MESSAGE_CONTENT","TEXT_MESSAGE_END","RUN_FINISHED")
}
```

Run `rtk just test ./internal/agent/... -run TestRuntimeKeepsToolNarrationOutOfTranscript` and record the leak before editing.

- [ ] **Step 2: Buffer each provider step until its outcome is known**

In `Runtime.execute`, `EventText` only appends to `text`; it emits no text event. After stream error/deadline handling and truncation processing, emit text start/content/end only if `len(toolCalls)==0`, once for the complete buffered final text. Preserve current visible truncation notice and refusal behavior. Use a separate `providerText:=text.String()` and `transcriptText:=""`; final steps assign both, tool-bearing steps retain providerText only in the current run's `conversation`/opaque `ProviderItems` and store an empty assistant Content with ToolCalls. Do not reintroduce narration on reload through `providerMessages`; stored tool calls/results remain sufficient continuation. Provider failure discards buffered text and emits the current sanitized RUN_ERROR. Preserve all tool and MCP-app events and their order.

New helper (called only for a successful final step) is complete:

```go
func emitFinalText(emitter *eventEmitter, messageID, text string) error {
    if text=="" {return nil}
    if err:=emitter.emit(events.NewTextMessageStartEvent(messageID,events.WithRole("assistant")));err!=nil {return err}
    if err:=emitter.emit(events.NewTextMessageContentEvent(messageID,text));err!=nil {return err}
    return emitter.emit(events.NewTextMessageEndEvent(messageID))
}
```

Remove `textStarted` and streaming `appendText` rather than leaving a dead path. Buffering deliberately delays final prose until the provider stop event; receipts still stream as tools finish. Keep ChatPage’s running activity/Loader visible until final text arrives or a terminal error/finish without text settles the turn; do not mark it done at the last tool receipt. Add chat.test.tsx fake-stream tests for the gap between final tool and buffered answer and for empty final/error/cancel states. The current output token bound limits the buffer. Do not implement heuristics such as hiding sentences beginning with “Fixing”.

- [ ] **Step 3: Verify final/error/reload outcomes**

Extend the scripted test with text-before-tool and text-after-tool within one step, multiple tool steps, text-only final, empty final, refusal, truncated partial tool call, provider error after text, cancellation and emitter failure. Existing `TestRuntimeEmitsStandardAGUISequence` and provider token-limit tests remain. Store/reload a real completed run and assert intermediate assistant Content is empty, final Content visible and tool/app activities survive. Tests should assert behavior/event order, not buffer helper internals.

```bash
rtk just test ./internal/agent/...
rtk just fmt
```

## Task 5: Return authoritative save checks and committed panel changes

**Files**
- Modify: `internal/dashboard/service.go`, `internal/dashboard/service_test.go`, `internal/mcp/dashboards.go`, `internal/mcp/panels.go`, `internal/mcp/panels_test.go`.
- Create: `internal/dashboard/changes.go`, `internal/dashboard/changes_test.go`, `internal/mcp/receipts_test.go`.
- Generated: MCP tool reference through `just docs-generate`.

Budget: $0. No schema or migration change. Reuse immutable version snapshots and existing authors/messages.

- [ ] **Step 1: Test exact changes and check completeness**

Create the following complete change test first. Diff normalized committed specs, not requested operation payloads, because normalization/packing may alter them.

```go
package dashboard
import (
    "testing"
    "github.com/labstack/fanout/internal/panel"
)
func TestChangesNameOnlyCommittedPanelFields(t *testing.T) {
    a:=panel.Dashboard{Panels:[]panel.Panel{{ID:"latency",Title:"Latency",Viz:"stat",Unit:"ms"},{ID:"errors",Title:"Errors",Viz:"text",Content:"unchanged"}}}
    b:=panel.Dashboard{Panels:[]panel.Panel{{ID:"latency",Title:"Latency",Viz:"stat",Unit:"s"},{ID:"new",Title:"New",Viz:"text",Content:"note"}}}
    got:=Changes(a,b)
    if len(got.Panels)!=3 || got.Panels[0].PanelID!="latency" || got.Panels[0].Kind!="changed" || len(got.Panels[0].Fields)!=1 || got.Panels[0].Fields[0]!="unit" || got.Panels[1].PanelID!="new" || got.Panels[1].Kind!="added" || got.Panels[2].PanelID!="errors" || got.Panels[2].Kind!="removed" {t.Fatalf("changes=%+v",got)}
    if same:=Changes(a,a);len(same.Panels)!=0 || same.LayoutChanged {t.Fatal("no-op reports changes")}
}
```

Create MCP tests using `newPanelServer`/`ownerRequest`: create one text panel and one deliberately described empty spans panel; assert a `save_check` row for each, checked=true, elapsed time nonnegative, matching saved version and no invented fixes. Edit just the text panel and assert one changed chip, `base_version=1`, version=2. Validate failure produces no save receipt/version. Engine error/check timeout returns a successful save with checked=false and a visible reason, **never** successful validation. Test missing/duplicate result IDs, failing panel, stale base_version, cross-owner get/replace and concurrent writer. Register a blocking validator/test synchronization to arrange a concurrent save; returned receipt must still identify its own committed version, not a later `s.get` result.

- [ ] **Step 2: Implement the wire data and deterministic diff**

Add this complete diff module; JSON field names are the public panel contract. Report authored top-level fields (`thresholds`, `query`, `options`, width/height/title/unit etc.) for changes; the UI can label `latency.thresholds` without claiming which element was fixed. Exclude physical `grid` from per-panel fields/chips; aggregate all grid changes into exactly one `layout_changed` flag, including server packing after add/remove. Authored panel order remains position_changed. No untouched neighbor gets a grid chip. Added/changed use after-order; removals use before-order. Dashboard-wide changes use separate `dashboard_fields`, not a fake panel.

```go
package dashboard
import (
    "bytes"
    "encoding/json"
    "sort"
    "github.com/labstack/fanout/internal/panel"
)
type PanelChange struct {
    PanelID string `json:"panel_id"`
    Title string `json:"title"`
    Kind string `json:"kind"`
    Fields []string `json:"fields,omitempty"`
    PositionChanged bool `json:"position_changed,omitempty"`
}
func changedFields(a,b any) []string {
    rawA,_:=json.Marshal(a);rawB,_:=json.Marshal(b)
    var x,y map[string]json.RawMessage
    _=json.Unmarshal(rawA,&x);_=json.Unmarshal(rawB,&y)
    keys:=map[string]bool{};for k:=range x {keys[k]=true};for k:=range y {keys[k]=true}
    fields:=[]string{};for k:=range keys {if !bytes.Equal(x[k],y[k]) {fields=append(fields,k)}}
    sort.Strings(fields);return fields
}
type ChangeSet struct {
    Panels []PanelChange `json:"panels"`
    LayoutChanged bool `json:"layout_changed"`
}
func Changes(before,after panel.Dashboard) ChangeSet {
    old:=map[string]panel.Panel{};for _,p:=range before.Panels {old[p.ID]=p}
    nextIDs:=map[string]bool{};for _,p:=range after.Panels {nextIDs[p.ID]=true}
    oldOrder:=map[string]int{};nextOrder:=map[string]int{}
    for _,p:=range before.Panels {if nextIDs[p.ID] {oldOrder[p.ID]=len(oldOrder)}}
    for _,p:=range after.Panels {if _,ok:=old[p.ID];ok {nextOrder[p.ID]=len(nextOrder)}}
    seen:=map[string]bool{};out:=[]PanelChange{};layout:=false
    for _,p:=range after.Panels {
        seen[p.ID]=true
        prior,exists:=old[p.ID]
        if !exists {layout=layout||p.Grid!=nil;out=append(out,PanelChange{PanelID:p.ID,Title:p.Title,Kind:"added"});continue}
        if !bytes.Equal(mustJSON(prior.Grid),mustJSON(p.Grid)) {layout=true}
        prior.Grid=nil;p.Grid=nil
        fields:=changedFields(prior,p);position:=oldOrder[p.ID]!=nextOrder[p.ID]
        if len(fields)>0 || position {out=append(out,PanelChange{PanelID:p.ID,Title:p.Title,Kind:"changed",Fields:fields,PositionChanged:position})}
    }
    for _,p:=range before.Panels {if !seen[p.ID] {layout=layout||p.Grid!=nil;out=append(out,PanelChange{PanelID:p.ID,Title:p.Title,Kind:"removed"})}}
    return ChangeSet{Panels:out,LayoutChanged:layout}
}
func mustJSON(v any) []byte {b,err:=json.Marshal(v);if err!=nil {panic(err)};return b}
```

Extend `dashboardOutput` with `Receipt *dashboardReceipt` tagged `json:"receipt,omitempty"`; reads leave it nil. New types in `internal/mcp/dashboards.go`:

```go
type savedPanelCheck struct {
    ID string `json:"id"`
    Status string `json:"status"`
    Rows int `json:"rows"`
    Diagnosis string `json:"diagnosis,omitempty"`
    Error string `json:"error,omitempty"`
    ElapsedMS int64 `json:"elapsed_ms"`
}
type saveCheck struct {
    Checked bool `json:"checked"`
    Reason string `json:"reason,omitempty"`
    ElapsedMS int64 `json:"elapsed_ms"`
    Panels []savedPanelCheck `json:"panels"`
}
type dashboardReceipt struct {
    BaseVersion int `json:"base_version"`
    Version int `json:"version"`
    Changes []dashboard.PanelChange `json:"changes"`
    LayoutChanged bool `json:"layout_changed"`
    DashboardFields []string `json:"dashboard_fields,omitempty"`
    SaveCheck saveCheck `json:"save_check"`
}
```

`panel.Result.ElapsedMS` is int64 and `Frame.Rows` is int; elapsed aggregate is `time.Since(start).Milliseconds()`, not a sum of parallel durations. Existing text results are ok with nil Frame; report rows=1 only for nonempty content, identifying content in the scorer rather than claiming a telemetry row. Never reinterpret no matching telemetry as healthy zero. `saved` retains `saveCheckBudget=8s`, returns structured unchecked reason for nil executor/error/timeout/incomplete IDs, sanitizes errors with `panel.SafeError`, and preserves warning summary. No retry that creates another saved version. Tests remove a panel and pack its neighbors: one removed chip, layout_changed=true, no unchanged-neighbor grid chips. Width/height/title/order edits retain authored chips. The eval scorer independently checks allowed layout changes only for placement-changing edits, mirrors the same authored-versus-grid separation and rejects unrelated authored changes; it never labels server packing as a changed panel.

Extend `PreviewOutput` with total `elapsed_ms` and each `PanelPreview` with `elapsed_ms`, populated from executed results; invalid/not_run records say so, not a zero-time successful preview. These are product snake_case fields. Task 6 derives correction transitions from preview problems; do not add a model-supplied “fixes applied” string to a trusted server receipt.

- [ ] **Step 3: Make save return values and diffs race-safe**

Add new service methods `CreateWithChanges`, `ReplaceWithChanges`, `EditWithChanges` returning `(Mutation,error)`, where `Mutation{Record Record, Before panel.Dashboard, BaseVersion int}` is built inside the successful optimistic attempt. Existing Create/Replace/Edit are wrappers that call them and return Mutation.Record, preserving the current API caller signatures. This is active API sharing, not an old-contract alias. Change internal `write` return to `(Record,bool,error)`; after update/version insertion/prune, call `q.GetDashboard` **inside that transaction**, decode the row, then commit and return that exact Record. Extract existing get-row decoding into one unexported helper reused by both paths. Create likewise reads its inserted row in its transaction before commit, capturing the query-computed is_default flag. Remove post-commit `s.get`; no result promises it is the latest version after another writer wins.

Store a deep copy of Before before Apply: operations may modify slice backing arrays. Marshal/decode normalized specs or copy full nested fields; shallow-copying the dashboard is insufficient. Retries recompute Before from the successful base. MCP uses the WithChanges methods; `saved` accepts Mutation, derives `diff:=Changes(Before,Record.Spec)`, `receipt.changes=diff.Panels`, `receipt.layout_changed=diff.LayoutChanged` and dashboard-wide changed fields (exclude panels). HTTP handlers still return Record. Test immutability, optimistic retry, explicit stale failure and interleaved saves. The already-existing immutable version row is sufficient; add no checksum replacement or receipt table.

```bash
rtk just test ./internal/dashboard/... ./internal/mcp/... ./internal/agent/... ./internal/api/...
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

Report contract keys, check failures and race test output. Public UI/eval consumers must not infer checks from prose warnings.

## Task 6: Show a quiet live build receipt, edit chips and request provenance

**Files**
- Modify: `ui/host/src/dashboard-tool-result.ts`, `ui/host/src/dashboard-tool-result.test.ts`, `ui/host/src/chat.tsx`, `ui/host/src/App.tsx`, `ui/host/src/rail.tsx`, `ui/host/src/app-context.ts`.
- `ui/host/src/dashboard-tool-result.test.ts` is **Create**, not an existing test; the other paths above exist.
- Modify: `internal/dashboard/service.go`, `internal/dashboard/identity.go`, `internal/agent/runtime.go`, `internal/agent/tools.go`, `internal/mcp/dashboards.go`, `internal/db/queries/dashboards.sql`, `internal/db/migrations_test.go`, `ui/host/src/dashboards/api.ts`.
- Create: `internal/db/migrations/20261008000001_dashboard_origins.sql`, `internal/dashboard/origin_test.go`; regenerate `internal/db/generated/` through db-gen.
- Create: `ui/host/src/dashboard-receipt.ts`, `ui/host/src/dashboard-receipt.test.ts`, `ui/host/src/dashboard-receipt-view.tsx`, `ui/host/src/dashboard-receipt-view.test.tsx`.
- Private collector extension: `.superpowers/replay/m2-browser.mjs`, its existing assertion tests plus new `.superpowers/replay/m3-receipts.test.mjs`.

Budget: $0; scripted seeded histories/tool results cover live rendering without a model call. Controller verifies those seeded results through the real transcript/iframe in both themes.

- [ ] **Step 1: Test observed progress and corrections**

Derive the build from the current user turn's assistant calls and matching tool results. New `receiptForTurn(messages,turnID)` returns schema/context/draft/validation/preview/save stages plus corrections, save link and changes. Join by call ID; never trust a dashboard-shaped result from a read, a tool name string embedded in text, or a different user turn. Schema stage comes from successful `get_telemetry_schema`; context from successful `get_intelligence_snapshot`/app telemetry calls and observed deploy/anomaly data; draft count from actual preview/save input; validation problems from `preview_panels`; correction means a named problem present in an earlier preview and absent in a later successful preview of that same panel. If a panel was removed, label removal, not a validation fix. Display server save check state separately from an earlier preview.

Complete label code/test to seed the component contract:

```ts
export type Change={panel_id:string;title:string;kind:'added'|'changed'|'removed';fields?:string[];position_changed?:boolean};
export const changeLabel=(c:Change):string=>c.kind==='added'?`+ ${c.title}`:c.kind==='removed'?`− ${c.title}`:`~ ${c.panel_id}${c.fields?.length?'.'+c.fields.join(', '):''}${c.position_changed?' · order':''}`;
export type Correction={panel_id:string;path:string;message:string};
export function resolvedProblems(before:Correction[],after:Correction[],retainedIDs:Set<string>):Correction[] {
  return before.filter(p=>retainedIDs.has(p.panel_id) && !after.some(q=>q.panel_id===p.panel_id && q.path===p.path));
}
```

```ts
import { expect,it } from 'vitest';
import { changeLabel,resolvedProblems } from './dashboard-receipt';
it('uses signed panel change chips and actual resolved validation paths',()=>{
  expect(changeLabel({panel_id:'pool',title:'Pool',kind:'added'})).toBe('+ Pool');
  expect(changeLabel({panel_id:'latency',title:'Latency',kind:'changed',fields:['thresholds']})).toBe('~ latency.thresholds');
  const problem={panel_id:'latency',path:'panels[0].query.measures[0]',message:'Unknown field'};
  expect(resolvedProblems([problem],[],new Set(['latency']))).toEqual([problem]);
  expect(resolvedProblems([problem],[],new Set())).toEqual([]);
  expect(resolvedProblems([problem],[problem],new Set(['latency']))).toEqual([]);
});
```

Add transcript fixtures with schema→bad preview→corrected preview→create, read-only tool result, failed save, parallel previews, multiple builds, malicious HTML titles and reload. Assert stage times/counts, per-panel statuses, correction names, exact version link and no duplicate final card. Failed or interrupted tool stages stay visibly incomplete. Persisted messages reconstruct the same receipt; no transient browser-only source of truth.

- [ ] **Step 2: Render compact progress and final changes**

`DashboardReceiptView` uses existing Mantine Group/Stack/Text/Badge/Collapse/Button styling from the chat card; no new palette. One `role="status" aria-live="polite"` summary per turn with completed stage count; detailed stage list is expandable. Show “Read telemetry · N panels · Checked in X s · Saved vN” only for those observed outcomes, plus a dashboard link. During execution, update this receipt in place as tool results arrive. Do not announce every token. Corrections and authored-field chips are a short wrap row; show layout_changed once as “Layout adjusted” without per-neighbor grid chips; long details collapse behind “Details”. Errors/unchecked/empty explanations must remain visible even when details are collapsed. Existing final two/three-sentence answer remains; no duplicative panel inventory prose.

Change `dashboardToolResult` to require the Task 5 receipt shape for create/edit/replace mutations. Task 7 owns adding `restore_dashboard_version` recognition and tests. Return `{id,name,version,label,receipt}`; no fallback to the M2 record-only card. Version links open the current dashboard with an explicitly labeled saved-version receipt; do not add a URL pretending the existing API can view an old version. Historical spec viewing is Task 8. Sanitize content with React text nodes; never render chip labels through HTML.

Persist the original build request so the rail works across threads/reload. Add one forward migration and extend the checksum ledger with its actual new SHA256; published entries stay unchanged:

```sql
-- +goose Up
CREATE TABLE dashboard_origins (
    dashboard_id TEXT PRIMARY KEY REFERENCES dashboards(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL REFERENCES agui_threads(thread_id) ON DELETE CASCADE,
    message_id TEXT NOT NULL,
    request_excerpt TEXT NOT NULL
);

```

New Go `BuildOrigin{ThreadID,MessageID,RequestExcerpt string}` has snake_case JSON tags. `WithBuildOrigin`/`BuildOriginFromContext` in identity.go carry server-derived context. Runtime sets it from the last user message in the authoritative seed and current threadID, truncating excerpt to280 Unicode characters. New `BuildOriginMetaKey="io.fanout/build-origin"` is injected by ToolRegistry from that context, never copied from model input. MCP accepts this metadata only on its in-process path with authenticated owner injection, **never** when OAuth TokenInfo is present; remote creates have no fabricated conversation origin. Extend identity.go’s existing trust-model invariant comment to both io.fanout/owner-id and io.fanout/build-origin: values are injected from authenticated in-process context, remote TokenInfo takes precedence, and remote metadata cannot fabricate ownership/provenance. Verify referenced thread.owner_id equals resolved owner inside the create transaction. Insert the origin only for create; edits/replace/restore cannot replace it. Any failure rolls back both dashboard and origin. Add the storage/auth/rollback failing tests in Step 1 before this migration is applied.

Add generated query in dashboards.sql:

```sql
-- name: InsertDashboardOrigin :execrows
INSERT INTO dashboard_origins (dashboard_id, thread_id, message_id, request_excerpt)
SELECT sqlc.arg(dashboard_id), sqlc.arg(thread_id), sqlc.arg(message_id), sqlc.arg(request_excerpt)
WHERE EXISTS (SELECT 1 FROM agui_threads WHERE thread_id = sqlc.arg(thread_id) AND owner_id = sqlc.arg(owner_id))
  AND EXISTS (SELECT 1 FROM dashboards WHERE id = sqlc.arg(dashboard_id) AND owner_id = sqlc.arg(owner_id));
```

Check affected rows=1 for a supplied valid origin; spoofed/nonowned origin cannot silently attach. Extend existing ListDashboards SELECT with LEFT JOIN dashboard_origins and optional origin fields, retaining d.owner_id filter/order. `dashboard.Summary.Origin *BuildOrigin` and TS DashboardSummary.origin feed rail's “Built from” excerpt and `/chat/$threadId` link; no per-dashboard/thread N+1. Live receipt reconstruction still joins successful create to the preceding turn. Test cross-thread/reload, Unicode truncation, same-owner enforcement, remote-meta spoofing, create rollback, edit preserves origin, deletion of dashboard/thread cascades origin, and a hand-created/external board has no invented request. A deleted source thread removes provenance rather than retaining its private text after deletion. No backfill/inference from a recent edit.

- [ ] **Step 3: Verify and hand the controller both-theme checks**

Extend private collector with `build_receipt`, `receipt_corrections`, `edit_chips`, `narration`, `request_provenance`, using seeded standard AG-UI tool events and Task 5 results. Add assertion tests rejecting “unchecked” receipts labeled checked, mismatched call IDs and a stray intermediate paragraph. Controller runs these checks and hands-on Chrome keyboard/expand/reload checks in both themes; Codex only runs syntax/assertion tests.

```bash
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
rtk proxy node --check .superpowers/replay/m2-browser.mjs
rtk proxy node --test .superpowers/replay/m3-receipts.test.mjs
```

Also run `rtk just db-gen`, `rtk just test ./internal/db/... ./internal/store/... ./internal/dashboard/... ./internal/mcp/... ./internal/agent/...`, `rtk just docs-generate` and `rtk just fmt` for provenance storage/contract changes. Report the new ledger entry and generated-query diff.

## Task 7: Expose owner-scoped version history and restore to MCP

**Files**
- Modify: `internal/mcp/dashboards.go`, `internal/mcp/panels_test.go`, `internal/agent/tools_integration_test.go`.
- Modify: `ui/host/src/app-context.ts` for plain-language history/restore activity labels; `ui/host/src/dashboard-tool-result.ts` and `ui/host/src/dashboard-tool-result.test.ts` for restore receipt recognition; no protocol tool name in product copy.
- Create: `internal/mcp/history_test.go`.
- Generated: MCP tool reference.

Budget: $0. The HTTP routes and SQLite version storage already exist; do not introduce a second history store.

- [ ] **Step 1: Add a failing history/restore test**

```go
package mcp
import (
    "testing"
    "github.com/labstack/fanout/internal/panel"
)
func TestDashboardHistoryToolsAppendRestoreVersion(t *testing.T) {
    s:=newPanelServer(t)
    _,created,err:=s.dashboardCreate(t.Context(),ownerRequest(),DashboardCreateInput{Dashboard:panel.Dashboard{Name:"History",Panels:[]panel.Panel{{ID:"note",Title:"Note",Viz:"text",Content:"first"}}}})
    if err!=nil {t.Fatal(err)}
    _,restored,err:=s.dashboardRestore(t.Context(),ownerRequest(),DashboardRestoreInput{ID:created.Dashboard.ID,Version:1})
    if err!=nil || restored.Dashboard.Version!=2 {t.Fatalf("restored=%+v err=%v",restored,err)}
    _,history,err:=s.dashboardVersions(t.Context(),ownerRequest(),DashboardIDInput{ID:created.Dashboard.ID})
    if err!=nil || len(history.Versions)!=2 || history.Versions[0].AuthorKind!="agent" || history.Versions[0].Message!="Restored version 1" {t.Fatalf("history=%+v err=%v",history,err)}
    for _,name:=range []string{"list_dashboard_versions","restore_dashboard_version"} {if RequiredToolScope(name)=="" {t.Fatalf("missing owner scope: %s",name)}}
}
```

Add OAuth tests: TokenInfo scope overrides spoofed Meta owner; a telemetry-only token cannot list/restore; another owner sees not-found; version<=0/missing/pruned fails; restore is not exposed as read-only. Verify in-process runtime ownership too.

- [ ] **Step 2: Register real history tools and return normal mutation receipts**

Append tool catalog indexes and registrations; `RequiredToolScope` already walks that catalog. `list_dashboard_versions` uses ReadOnlyHint=true, OpenWorldHint=false. `restore_dashboard_version` uses DestructiveHint=true, OpenWorldHint=false, no IdempotentHint (each successful call creates another version). Complete list adapter:

```go
type DashboardRestoreInput struct {
    ID string `json:"id" jsonschema:"Dashboard ID"`
    Version int `json:"version" jsonschema:"Positive stored version to restore; restoring creates a new version"`
}
type dashboardVersionsOutput struct { Versions []dashboard.VersionInfo `json:"versions"` }
func (s *Server) dashboardVersions(ctx context.Context,req *mcp.CallToolRequest,input DashboardIDInput)(*mcp.CallToolResult,dashboardVersionsOutput,error) {
    owner,err:=dashboardOwner(req);if err!=nil {return nil,dashboardVersionsOutput{},err}
    versions,err:=s.dashboards.Versions(ctx,owner,strings.TrimSpace(input.ID))
    if err!=nil {return nil,dashboardVersionsOutput{},dashboardToolError(err)}
    return summary(fmt.Sprintf("Found %d saved versions.",len(versions))),dashboardVersionsOutput{Versions:versions},nil
}
```

Add service `RestoreWithChanges` parallel to Task 5's WithChanges methods; existing Restore wraps it. Its owner-scoped historical spec read feeds the same update transaction/optimistic attempt, author and `Restored version N` message; MCP `dashboardRestore` resolves owner, rejects nonpositive version, calls it with agentAuthor and returns `saved(ctx,"Restored",mutation)`. Default restore-to-latest semantics remain existing behavior. No new route, alias or user-supplied owner field. Catalog descriptions tell the agent to restore only on an explicit user request and distinguish the target historical version from newly saved version. Add dashboardToolResult recognition here, returning the same receipt/version shape for restore; test live/reloaded restore, failed/cross-turn results and no record-only fallback. Run host test/lint/build alongside Go contract checks.

```bash
rtk just test ./internal/mcp/... ./internal/dashboard/... ./internal/agent/... ./internal/api/...
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

## Task 8: Add version history, inspectable changes and restore UI

**Files**
- Modify: `ui/host/src/dashboards/page.tsx`, `ui/host/src/dashboards/toolbar.tsx`, `ui/host/src/dashboards/toolbar.test.tsx`, `ui/host/src/dashboards/api.ts`, `ui/host/src/dashboards/api.test.ts`, `internal/dashboard/service.go`, `internal/api/dashboard.go`, `cmd/fanout-docgen/groups.go` only if its route enumeration needs adjustment.
- Modify: `internal/db/queries/dashboards.sql`; regenerate `internal/db/generated/` with db-gen. `cmd/fanout-docgen/groups.go` requires no new prefix for an absolute Echo route; its retired Observability prefix was already removed in Task 2.
- Create: `ui/host/src/dashboards/history.tsx`, `ui/host/src/dashboards/history.test.tsx`, `internal/dashboard/history_test.go`, `internal/api/dashboard_history_test.go`.
- Generated: HTTP routes reference; private collector history checks.

Budget: $0. Existing list includes author_kind, author_id, message and created_at. Add one owner-scoped historical-spec read so “what changed” has evidence; no new table.

- [ ] **Step 1: Test historic reads and restore before building the drawer**

Add service `VersionRecord(ctx,owner,id,version)(VersionRecord,error)`, route `GET /api/dashboards/:id/versions/:version`, and client `getVersion(id,version,signal?)`. Return historical spec/name/description/version with the dashboard identity; timestamps come from the historical version row, not today's record. Current generated `GetDashboardVersion` returns only spec JSON: add `GetDashboardVersionRecord` in `internal/db/queries/dashboards.sql` to select spec_json, author_kind/id/message/created_at with an owner-joined dashboards predicate. Run `just db-gen`; no new migration beyond Task 6. The response includes `{dashboard:Record, author_kind, author_id, message, created_at, changes:PanelChange[], layout_changed:boolean, dashboard_fields:string[], changes_available:boolean}`. Go dashboard.Changes computes selected version versus version−1; version 1 compares against an empty spec, while a pruned predecessor returns changes_available=false and an explicit unavailable UI state. No TypeScript spec diff. This new query is justified to show exact historic changes without N+1 raw reads or invented labels. Limit history to existing keepVersions=100.

```sql
-- name: GetDashboardVersionRecord :one
SELECT d.id, d.is_default, d.created_at AS dashboard_created_at,
       v.version, v.spec_json, v.author_kind, v.author_id, v.message,
       v.created_at AS version_created_at
FROM dashboard_versions v JOIN dashboards d ON d.id = v.dashboard_id
WHERE d.id = sqlc.arg(dashboard_id)
  AND d.owner_id = sqlc.arg(owner_id)
  AND v.version = sqlc.arg(version);
```

Test correct owner/historical content, cross-owner not-found, invalid/nonexistent/pruned version, ordering and restore adds currentVersion+1 with the authenticated user author/message. API tests cover authorization and route classification. Add complete service regression using existing helpers:

```go
func TestHistoricalSpecReadIsOwnerScopedAndImmutable(t *testing.T) {
    s:=newTestService(t)
    first,err:=s.Create(t.Context(),"owner",textSpec("History"),agent);if err!=nil {t.Fatal(err)}
    changed:=textSpec("History");changed.Panels[0].Content="second"
    if _,err=s.Replace(t.Context(),"owner",first.ID,changed,first.Version,agent,"Updated note");err!=nil {t.Fatal(err)}
    old,err:=s.VersionRecord(t.Context(),"owner",first.ID,1)
    if err!=nil || old.Dashboard.Version!=1 || old.Dashboard.Spec.Panels[0].Content!="hello" {t.Fatalf("old=%+v err=%v",old,err)}
    if _,err=s.VersionRecord(t.Context(),"other",first.ID,1);!errors.Is(err,ErrNotFound) {t.Fatalf("ownership error=%v",err)}
}
```

Place this test in package dashboard, importing errors/testing. Use VersionRecord as the sole new service read, actively called by the HTTP handler; add no test-only exported Version wrapper.

- [ ] **Step 2: Implement the owner-scoped read and history drawer**

Add `VersionRecord(ctx,owner,id,version)(VersionRecord,error)` with this row-decoding implementation in service.go (context/sql/json/errors/generated/panel imports already exist):

```go
type VersionRecord struct {
    Dashboard Record `json:"dashboard"`
    AuthorKind string `json:"author_kind"`
    AuthorID string `json:"author_id,omitempty"`
    Message string `json:"message,omitempty"`
    CreatedAt string `json:"created_at"`
    Changes []PanelChange `json:"changes"`
    LayoutChanged bool `json:"layout_changed"`
    DashboardFields []string `json:"dashboard_fields"`
    ChangesAvailable bool `json:"changes_available"`
}
func (s *Service) VersionRecord(ctx context.Context,owner,id string,version int)(VersionRecord,error) {
    if version<=0{return VersionRecord{},ErrNotFound}
    r,err:=generated.New(s.db).GetDashboardVersionRecord(ctx,generated.GetDashboardVersionRecordParams{DashboardID:id,OwnerID:owner,Version:int64(version)})
    if errors.Is(err,sql.ErrNoRows){return VersionRecord{},ErrNotFound};if err!=nil{return VersionRecord{},err}
    var spec panel.Dashboard;if err:=json.Unmarshal([]byte(r.SpecJson),&spec);err!=nil{return VersionRecord{},err}
    record:=Record{ID:r.ID,Name:spec.Name,Description:spec.Description,IsDefault:r.IsDefault==1,Version:int(r.Version),Spec:spec,CreatedAt:r.DashboardCreatedAt,UpdatedAt:r.VersionCreatedAt}
    out:=VersionRecord{Dashboard:record,AuthorKind:r.AuthorKind,AuthorID:r.AuthorID,Message:r.Message,CreatedAt:r.VersionCreatedAt,Changes:[]PanelChange{},DashboardFields:[]string{}}
    before:=panel.Dashboard{}
    if version>1 {
        prior,err:=generated.New(s.db).GetDashboardVersionRecord(ctx,generated.GetDashboardVersionRecordParams{DashboardID:id,OwnerID:owner,Version:int64(version-1)})
        if errors.Is(err,sql.ErrNoRows) {return out,nil};if err!=nil {return VersionRecord{},err}
        if err:=json.Unmarshal([]byte(prior.SpecJson),&before);err!=nil {return VersionRecord{},err}
    }
    diff:=Changes(before,spec);out.Changes=diff.Panels;out.LayoutChanged=diff.LayoutChanged
    before.Panels=nil;spec.Panels=nil;out.DashboardFields=changedFields(before,spec);out.ChangesAvailable=true
    return out,nil
}
```

The handler is the active consumer; retain only used exports. Test Go changes for v1, a title edit, a packed removal and a pruned predecessor, and assert GET wire fields match save receipt semantics. Never rewrite or normalize historical bytes on a read. Historical Record.IsDefault is presentation metadata of the currently owned board, not a promise about its past default status; do not label it as historical. The board's creation timestamp stays its creation time; the version's time/UpdatedAt comes from the version row. Never feed a historical view into today's query cache under the current key. Handler `version` parses a positive path version, resolves RequestOwner and returns s.VersionRecord through dashboardError on failure. Register with the existing own middleware, and add its read classification/docgen test.

`Toolbar` adds History button and `onHistory()`. `HistoryDrawer({id,currentVersion,onRestored})` queries `['dashboard-versions',id]` with listVersions only while open. List version/date/“Agent”, “You” or “System”, and stored edit message. A selected version loads getVersion; render its server-provided changes/layout_changed/dashboard_fields using Task 6 chip labels, show read-only Spec/Data summary and never auto-run all historical panels. Restore button reads “Restore version N”; explicit click is authorization. Disable while pending; on success set `['dashboard',id]` to returned Record and invalidate dashboardsKey, version list, panel results/annotations/variables through existing hooks. Preserve current URL time/vars; drop only nonexistent focused-panel/drill state. Show success “Restored vN as vM”; on error keep drawer and current view, offer retry only by deliberate new click.

Use abort signals for selected historical requests; switching selection must not display a late prior response. Do not invent “person” names from an opaque author_id. Test agent/person/message labels, loading/empty/error/not-found, read-only older version, one POST on double click, restore conflict/failure and cache updates. Use mocked APIs/React Query/Mantine with createRoot as existing dashboard tests do. Full UI regression runs below, then controller collector `version_history`,`restore_version` in both themes and hands-on selection/restore/back-navigation.

```bash
rtk just db-gen
rtk just test ./internal/db/... ./internal/store/... ./internal/dashboard/... ./internal/api/...
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
rtk just docs-generate
rtk just docs-generate-check
rtk just fmt
```

## Task 9: Complete full-screen and make Explain precise and read-only

**Files**
- Modify: `ui/host/src/dashboards/grid.tsx`, `ui/host/src/dashboards/panel-card.tsx`, `ui/host/src/dashboards/fragment-view.tsx`, `ui/host/src/dashboards/page.tsx`, `ui/host/src/app-context.ts`, `internal/agent/runtime.go`, `internal/agent/tools.go`, `internal/agent/dashboard_guidance_test.go`, `ui/host/src/App.tsx`, `ui/host/src/chat.test.tsx`. Create `internal/agent/answer_only_test.go` and `ui/host/src/answer-only.test.tsx` for enforcement/request-capture tests.
- Create: `ui/host/src/dashboards/explain.ts`, `ui/host/src/dashboards/explain.test.ts`, `ui/host/src/dashboards/fullscreen.test.tsx`.
- Private collector: `fullscreen_panel`,`explain_panel` checks.

Budget: $0. Use captured results and mocked agent submissions. Live authoring/explanation cost is included in Task 14’s ledger/p95 gate if run there.

- [ ] **Step 1: Test pinned Explain context and no mutation request**

```ts
import { expect,it } from 'vitest';
import { explainPrompt } from './explain';
it('pins the observed window, variables and error without asking to fix',()=>{
  const prompt=explainPrompt({dashboard_id:'board',version:7,panel_id:'latency',title:'Latency',from_ms:0,to_ms:3600000,vars:{service:['checkout']},error:'Invalid measure'});
  expect(prompt).toContain('1970-01-01T00:00:00.000Z');
  expect(prompt).toContain('1970-01-01T01:00:00.000Z');
  expect(prompt).toContain('version 7');expect(prompt).toContain('checkout');
  expect(prompt).toContain('Do not create, edit, replace or restore');
  expect(prompt).not.toContain('Please fix');
});
```

Complete implementation (the prompt is a user action context, never a hidden system instruction):

```ts
export type ExplainContext={dashboard_id?:string;version?:number;panel_id:string;title:string;from_ms?:number;to_ms?:number;vars:Record<string,string|string[]>;error?:string;spec?:unknown};
export function explainPrompt(c:ExplainContext):string {
  const window=Number.isFinite(c.from_ms)&&Number.isFinite(c.to_ms)?`${new Date(c.from_ms!).toISOString()} to ${new Date(c.to_ms!).toISOString()}`:'No executed window is available; say that explicitly.';
  return `Explain the panel ${JSON.stringify(c.title)} (panel id ${c.panel_id}${c.dashboard_id?`, dashboard id ${c.dashboard_id}, version ${c.version}`:''}). Observed window: ${window}. Resolved variables: ${JSON.stringify(c.vars)}. ${c.error?`Observed error: ${JSON.stringify(c.error)}. `:''}${c.spec?`Panel specification: ${JSON.stringify(c.spec)}. `:''}Answer what it shows and what evidence supports unusual behavior. This is an explanation request. Do not create, edit, replace or restore a dashboard. If it fails, explain the error and suggest a correction without saving it.`;
}
```

Use `PanelResult.from_ms/to_ms` (already adjusted for a panel time override), not JSON of a relative “1h” that moves later. Preserve exact nanosecond absolute spec bounds separately when available; result millisecond fields must not claim nanosecond fidelity. Saved-dashboard Explain includes id/version/panel id plus observed vars. Chat fragments hide Explain entirely, including full-screen, and every mutating action. Host uses existing onOpenChat, extended to carry a turn-scoped answer-only request; no iframe Explain/sendMessage path.

Choose **runtime refusal** for answer-only turns: send product `answer_only:true` inside AG-UI’s protocol `forwardedProps`, carry it through Session in App.tsx (`pendingPromptRef`, openChat→send→run→HttpAgent.runAgent), and validate its boolean type in Runtime.Run before StartRun. Extend FanoutAppContext send/openChat signatures with optional `{answer_only?:boolean}`; store pending text/options together and clear them after consumption. Preserve retry options for the same turn, but clear answer_only on a subsequent ordinary send/new thread. Set a run-context flag, never a prompt-text heuristic; refuse create_dashboard/edit_dashboard/replace_dashboard/restore_dashboard_version and every other mutating tool **before Execute**. Use a central behavior classification from MCP readOnlyHint metadata plus explicit reviewed read-only/mutation entries when a tool lacks it; unknown tools are refused for an answer-only turn. Read-only telemetry and get/list tools remain available.

Prompt wording supports this enforcement. A scripted fake provider deliberately calls each mutator on an answer-only turn: tool error, zero Execute invocations, no new dashboard version, followed by a readable explanation. Test malformed forwardedProps, client request capture, retry retaining the flag and the next ordinary edit permitted. This is a turn-scoped request, not persisted user text that can switch another run’s mode.

Keep the error action separately labelled **“Ask Fanout to fix it”**, with a distinct onFix callback/edit prompt, shown only on saved dashboards when agentAvailable and dashboard-manage permission are true. It sends an ordinary explicit edit request with dashboard/version/panel/error context; it does not set answer_only. onExplain is optional and separate from onFix. Tests cover viewer/manager, no fix action for unsaved/chat fragments, separate prompts/flags and full-screen parity. Explain stays answer-only for managers too. Extend guidance that Explain is answer intent without relying on it to block writes.

- [ ] **Step 2: Verify full-screen shared results, links and focus**

Existing `PanelGrid` Modal stays URL-backed with `view=panel_id`; fragment full-screen is local state, not a fake dashboard ID. Render the same PanelCard and result object; no additional queryPanels or refresh timer. Preserve Inspect/Data/Spec, annotation context, comparison, click/filter/drill, and dashboard Explain (chat hides it). Give dialog a panel title accessible name, focus the close control, trap focus and restore it to the invoking menu/keyboard control on Escape/close; direct-link open restores to the panel menu when no prior focus exists. Browser Back closes an URL-backed view without losing time/vars/compare, unknown panel ID shows an explicit missing-panel state rather than another panel.

Avoid two active ECharts with identical panel audit identity or linked group: unmount the grid panel's visualization while its modal copy is mounted, while retaining its grid card/layout and fetching visibility. This prevents duplicate crosshair/audit results and extra canvas resources. Keep intersection/visibility updates including the focused panel. Test zero extra requests, compare/empty/error/stale/truncated, resize and dark theme, focus return/back, unknown ID and no mutation on Explain error.

```bash
rtk just test ./internal/agent/...
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
rtk just fmt
```

Controller checks full-screen in chat/dashboard, light/dark; saved-dashboard Explain captures answer_only=true and no mutation, while the manager-only fix request has its own label and ordinary edit intent. Confirm neither Explain nor any mutation action appears in chat. A test mock is not iframe acceptance evidence.

## Task 10: Add keyboard point/filter/drill, range controls and shortcut help

**Files**
- Modify: `ui/host/src/dashboards/echart-canvas.tsx`, `ui/host/src/dashboards/viz/timeseries.tsx`, `ui/host/src/dashboards/viz/bar.tsx`, `ui/host/src/dashboards/viz/analysis-chart.tsx`, `ui/host/src/dashboards/panel-card.tsx`, `ui/host/src/dashboards/grid.tsx`, `ui/host/src/dashboards/page.tsx`, `ui/host/src/dashboards/toolbar.tsx`, `ui/host/src/dashboards/fragment-view.tsx`.
- Modify: `ui/panels/compile.ts`, `ui/panels/analysis.ts`, `ui/panels/interaction.ts` only for shared interaction metadata; do not change query ranking or result format.
- Create: `ui/panels/keyboard.ts`, `ui/host/src/dashboards/keyboard.test.ts`, `ui/host/src/dashboards/chart-keyboard.tsx`, `ui/host/src/dashboards/chart-keyboard.test.tsx`, `ui/host/src/dashboards/shortcuts.ts`, `ui/host/src/dashboards/shortcuts.test.ts`, `ui/host/src/dashboards/shortcuts-help.tsx`.
- Private collector: `keyboard_point`,`keyboard_range`,`dashboard_shortcuts` checks.

Budget: $0. This task is three independently tested slices within one review gate; do not redesign charts or alter ranking to simplify navigation.

- [ ] **Step 1: Make compiled points keyboard-addressable**

The package-free `keyboard.ts` extracts point descriptors from the **compiled displayed series**, not all raw frame values. New interface:

```ts
export type PointEvent={name?:string;seriesName?:string;value?:unknown;data?:unknown;dataType?:string;interactive?:boolean};
export type KeyboardPoint={series_index:number;data_index:number;label:string;event:PointEvent};
export function keyboardPoints(series:readonly {name?:string;data?:readonly unknown[];interactive?:boolean}[]):KeyboardPoint[] {
  const out:KeyboardPoint[]=[];
  series.forEach((s,series_index)=>{
    if(!s.data || s.interactive===false)return;
    s.data.forEach((data,data_index)=>{
      const object=data!==null&&typeof data==='object'&&!Array.isArray(data)?data as {value?:unknown;name?:string}:undefined;
      const value=object?.value??data;
      if(value===null || value===undefined)return;
      out.push({series_index,data_index,label:`${s.name??'Series'} · point ${data_index+1}`,event:{seriesName:s.name,name:object?.name,value,data,interactive:s.interactive}});
    });
  });
  return out;
}
```

This is only candidate extraction. Pass every candidate through the same existing `pointSelection(panel,result,event)` or custom-series `data.selection` dispatcher used by mouse before offering drill/filter. Compilers explicitly mark previous/helper series `interactive:false`; preserve existing `isOtherSeries` rejection in the shared selection callback. Previous display suffix is currently ` · previous`, not `(previous)`. For current series set metadata and extend ChartEvent with an optional `interactive` flag so a compiler-marked current series is not misidentified merely by the previous-period suffix; mouse and keyboard both pass that flag. User labels and synthesized Other currently share the existing naming rule; changing engine Other identity is outside this task. Test the same rejection/selection for both input methods, including a literal current label ending in ` · previous`. No giant hidden DOM table of 200,000 cells.

Complete initial test, then add custom heatmap/histogram/scatter/state timeline and legitimate-name cases:

```ts
import { expect,it } from 'vitest';
import { keyboardPoints } from '../../../panels/keyboard';
it('preserves compiled data identity and displayed order',()=>{
  const datum={value:[10,25],selection:{dimensions:{service:'checkout'}}};
  const points=keyboardPoints([{name:'checkout',data:[null,datum]},{name:'empty',data:[]}]);
  expect(points).toHaveLength(1);expect(points[0].series_index).toBe(0);expect(points[0].data_index).toBe(1);
  expect(points[0].event.data).toBe(datum);
});
```

- [ ] **Step 2: Use the same callbacks for keyboard and mouse**

`ChartKeyboard` has a discoverable “Explore chart” button, series select, bounded point index input/previous/next buttons and a readable point summary (series, exact time/category, formatted value/unit). Enter invokes the existing click callback once. EChartCanvas dispatches highlight/showTip for the selected series_index/data_index and downplay/hideTip on exit; focus indicators must be visible in both themes. Updating options/resize recomputes candidates and clamps selection; it must not trigger an action automatically. Collapse controls until requested to preserve current chart space.

Add explicit “Zoom to range” start/end inputs for time charts; use available observed window bounds and UTC labels. Reject nonfinite/reversed/out-of-window ranges and call the existing onZoom once with numeric milliseconds; do not set ECharts dataZoom locally while URL range stays unchanged. Keyboard range uses the same dashboard search transition as brush, includes Back/zoom-out and comparison. Row tables/service-map/waterfall already have button/Enter behavior: test them, and repair any missing keyboard activation without adding a second interaction model.

In React tests mock ECharts dispatchAction and use actual focus/key events: choose series/point, Enter selection equals mouse `Selection`, no action for unsupported/previous/Other, range validation, option updates, cleanup and no document-wide interception. Ensure inspect data remains keyboard accessible. Shared FragmentView receives the same controls/callbacks so chat parity follows the one renderer.

- [ ] **Step 3: Scope dashboard shortcuts and add discoverable help**

Create `shortcuts.ts` complete eligibility helper:

```ts
export function shortcutKey(event:KeyboardEvent,modalOpen:boolean):string|null {
  const target=event.target;
  if(modalOpen || event.defaultPrevented || event.repeat || event.ctrlKey || event.metaKey || event.altKey || (event.shiftKey&&event.key!=='?'))return null;
  if(target instanceof Element && target.closest('input,textarea,select,[contenteditable="true"],[role="textbox"],[role="dialog"],[role="menu"]'))return null;
  return ['r','e','h','f','?'].includes(event.key)?event.key:null;
}
```

Page-level listener only exists while DashboardPage is active and focus is within its dashboard region. Map r→onRefreshNow, e→edit toggle, h→history, f→focused panel View, ?→ShortcutsHelp. f without a focused panel does nothing and help explains how to focus one. Ignore Shift except the ? chord, and global `/` stays owned by existing chat code. Escape relies on current topmost Mantine overlay, avoiding two simultaneous closes. Toolbar has “Keyboard shortcuts” button and accessible title; help lists these keys and chart Explore/range behavior in plain language. Do not hijack browser refresh/find/history modifier shortcuts or typing in chat/filters.

Test eligibility for every ignored context, modifier, repeat, inactive route and focused panel, one action, listener cleanup, help Tab/Escape/focus return. Controller runs keyboard-only chart/filter/drill/range/fullscreen/history and help in both themes and iframe; capture actual requested URL/body scope. This is functional accessibility, not a claim of a full WCAG audit.

```bash
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
```

## Task 11: Lock log-pattern context and fix two cheap M2 edge cases

**Files**
- Modify: `internal/panel/preview_patterns_test.go`, `internal/mcp/panels.go`, `ui/host/src/dashboards/search.ts`, `ui/host/src/dashboards/search.test.ts`, `ui/host/src/router.test.ts`, `internal/query/annotations.go`, `internal/query/annotations_test.go`, `internal/query/version_gate_test.go` (renamed in Task 2, TestVersionRollupBudgetStartsAfterGate).
- Extend the existing behavior-renamed `internal/query/version_gate_test.go`; do not recreate annotations_fix_test.go.
- Private collector Modify: `.superpowers/replay/m2-browser.mjs` and owning assertion tests for `empty_multiselect` and `pattern_context` checks; Task 13 only supplies cross-cutting evidence helpers later.
- Generated: MCP tool reference if context wording changes.

Budget: $0. Existing `compileRowsWhere` already returns severity/service for patterns; no optional `by` expansion and no spec v2.

- [ ] **Step 1: Extend executed pattern tests**

Retain `TestPreviewPatternContextEngine` and its redaction/tie cases. Add a namespace/time-restricted test with the same body_template in two namespaces, one out-of-window higher-severity event, tied dominant severities and tied services. Execute through the real `Executor.Run`, deterministic Parquet `newTestEngine`/`commit`; assert context only reflects in-scope redacted events, severity frequency then higher-severity tie, top-service count then ascending-name tie, count equals group size, trend<=240, and matching drill logs use the same redacted body. Test multiple services without losing one pattern grouping. The current five columns remain body_template/count/trend/severity/service; qualified logs AST behavior remains fail-closed and is explicitly deferred. Do not reimplement grouping in the browser.

Add clear guide text: “Patterns group only by body_template. Severity is the most frequent severity, ties choose the higher severity. Service is the most frequent service, ties choose ascending service name. Both describe the scoped, redacted events, not extra grouping dimensions.” The shared LogPatternsViz already displays them; add the pattern_context collector check and its fixture assertions here, verifying context in chat and dashboard in both themes.

- [ ] **Step 2: Preserve an explicit empty multi-select in links**

Use one canonical structured envelope for **all variable arrays** in `toSearchParams`: `out['var-'+name]=Array.isArray(value)?{values:value}:value`. Existing router already JSON-serializes objects and decodes object values, while scalar empty string and All stay primitive. In `parseSearch`, accept the envelope only when it has exactly key `values`, an array of strings, bounded to existing variable option limits; reject malformed object values. Remove repeated-array var decoding rather than maintaining two array encodings; the pre-release contract changes with tests, no compatibility path. Other repeated query parameters are unrelated router behavior and remain.

Complete behavioral test in existing search.test.ts:

```ts
it('round-trips All, empty, empty-string, one and several selections distinctly',()=>{
  const values:(string|string[])[]=['$__all',[],[''],['checkout'],['checkout','payments']];
  for(const value of values) {
    const vars={service:typeof value==='string'?value:[...value]};
    const raw=toSearchParams({vars});
    const encoded=JSON.parse(JSON.stringify(raw));
    expect(parseSearch(encoded).vars).toEqual(vars);
  }
  expect(parseSearch({'var-service':{values:[],extra:true}}).vars).toBeUndefined();
});
```

The production `VarValue` interface does not change. Add integration tests through the real router stringify/parse hooks (existing router.test.ts) and copied full-screen/compare/drill link, not just object round-trip. Keep exact absolute timestamp strings; do not Date-normalize URL nanoseconds. Test scalar values that resemble JSON/contain commas/brackets, option validation and removing an explicit empty chip. Add empty_multiselect collector interaction and assertion cases here: copied link reopens the empty selection in both themes; Task 13 does not duplicate this feature collection.

- [ ] **Step 3: Bound version-rollup waits separately from execution**

In `RefreshVersionRollup`, start a **20-second wait context** before `writeGate.LockContext` and `lockRollupParquetRead`; cancel it after both acquired. Release first lock if second fails. Start the existing **10-second query context** only after both gates are held. This preserves `TestVersionRollupBudgetStartsAfterGate` (holds the gate about 10.1s) while bounding a caller without deadline. Caller deadlines still win. Wait timeout is context.DeadlineExceeded, records the existing RollupError outcome/timeout warning, introduces no lock retry/backoff, and cannot authorize cleanup. Keep service/edge analytical write gates separate from completed-batch writes.

Define unexported `versionRollupWaitBudget=20*time.Second`; factor `refreshVersionRollup(ctx,waitBudget)` so the public method passes the constant and tests pass 20ms without a 20s sleep. Testing starts a goroutine holding the gate, uses channels for deterministic acquisition and release, calls with context.Background and asserts deadline/bounded duration/no leaked lock. Also cover second-gate timeout, caller cancellation and successful 10-second execution budget after wait. Do not mutate a package-global timeout concurrently in tests.

```bash
rtk just test ./internal/panel/... -run TestPreviewPatternContextEngine
rtk just test ./internal/panel/... ./internal/query/... ./internal/mcp/...
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
rtk just docs-generate
rtk just fmt
```

Report the deferred qualified-log SQL issue with its exact redaction-safe failure and M4 ownership. No late “quick fix” through text replacement of SQL names.

## Task 12: Measure and improve authoring against the public benchmark

**Files**
- Modify only if measured failures justify it: `internal/agent/runtime.go` (`systemPrompt` and `dashboardAnalysisGuidance`), `internal/agent/dashboard_guidance_test.go`, `internal/mcp/panels.go` (`baseSpecGuide`/`specGuide`), `internal/mcp/dashboards.go` (descriptions), `scripts/dashboard-eval/score.test.ts` only to fix a demonstrated scorer defect without weakening thresholds.
- Create: `docs/benchmarks/2026-10-agent-dashboards-m3.md` with public aggregate baseline/iteration results, no raw telemetry/prompts/hosts.
- Private evidence: `.superpowers/eval/` runs, cost ledger and Task 12 report (holdout outputs are never stored here).

Budget: **≤ $5 total**, not $5 per attempt: approximately $3 for one full ten-create/five-edit baseline and at most the remaining $2 for targeted benchmark reruns. A full rerun is allowed only when the per-prompt p95 gate fits the remaining shared task ledger. No paid judge and no OpenAI comparison run required; OpenAI default is still verified in config/tests. The run buys measured S1–S5 and identifies the smallest product guidance defect. Do not spend the remaining budget merely because it exists.

- [ ] **Step 1: Freeze the input and measure baseline through the promoted runner**

Controller prepares a disposable native instance with fresh control state and copied/replayed **format 3** demo telemetry. Record telemetry manifest hash, actual event-time from/to bounds, row/byte totals, retention, server version, model/provider, source hash and thread ownership. Use M2’s time-shifted replay: shift the immutable format-3 source uniformly by recorded shift_ns so its newest event ends just before replay/run start. Record original source hash, replay source/tool hash, shift_ns, target/latest instant and resulting event-time bounds in the manifest and each run artifact. Start all ten creates and five consecutive edits within <=60 minutes after replay; later iterations/final runs record their own shift and source identity. Add no product clock override; relative “right now/this hour” reads the running clock against freshly shifted telemetry. No control database/secrets copied. Runtime-injected provider credentials use official 1Password Environment mounts/injection if that is how the controller obtains them; values never enter Codex context or files. No product legacy reader/converter.

Controller command (all env values supplied privately, nonempty; the CLI rejects omissions):

```bash
rtk proxy bun scripts/dashboard-eval/main.ts --base "$FANOUT_EVAL_BASE" --cookies "$FANOUT_EVAL_COOKIES" --model-label claude-sonnet-5-5 --prompts-file "$FANOUT_BENCHMARK_PROMPTS" --edits-file "$FANOUT_EDIT_EXPECTATIONS" --snapshot-manifest "$FANOUT_SNAPSHOT_MANIFEST" --cost-ledger "$FANOUT_EVAL_COST_LEDGER" --budget-usd 5 --out .superpowers/eval/m3-iteration --set benchmark
```

Task 3's `--budget-usd` is the entire ledger cap shared across invocations, not a fresh balance on each CLI execution. Save timestamp is the first successful mutation result observation (a conservative upper bound after save/post-check), never the end of the final answer or a later list read. Failed saves/retries remain in the record. Validate every saved spec using the real validator/preview before scoring S3; invalid intermediate drafts corrected before save are receipt corrections, not S3 final failures. Save-check incomplete means S2 failed/missing. All ten must have conclusive evidence.

- [ ] **Step 2: Correct only generalizable failures, with a failing regression first**

For each measured defect, add a deterministic failing test at its actual layer. Examples: unsupported invented measure→guide/schema clarity; unrelated edit replaced spec→get-first/typed edit/base_version guidance; unexplained empty→authored panel description and diagnosis; single answer persisted board→explicit intent distinction. Do not add prompt-number branches, demo names, fixed query recipes or timing shortcuts that skip validation. Use `query_telemetry` for factual answer intent; overview/performance etc are fragment conveniences, no parallel chart payloads. Keep tool descriptions concise to reduce repeated token context, retain all v1 constraints and annotations/evidence cautions.

Targeted reruns use fresh disposable threads and the same immutable source with the run’s recorded time-shifted replay; S5 reruns are the full five-edit chain, not a favorable one-edit sample. Record which candidate each run tested. A targeted pass is diagnostic, never a replacement for all-ten final evidence. If S4 stays above45s, report measured failure; do not weaken threshold or remove the post-save check to make it pass. At cap, stop and report incomplete/failed plus next generalizable action for the controller; no unauthorized larger run. Final Task 14 verifies the final candidate afresh.

- [ ] **Step 3: Verify product changes and publish measured aggregates**

```bash
rtk just test ./internal/agent/... ./internal/mcp/... ./internal/panel/...
rtk proxy bun test scripts/dashboard-eval
rtk just docs-generate
rtk just fmt
```

Benchmark doc reports actual numbers (10/10 or failures, good/total panels,0 final validation failures or measured count, median/prompt timings,5/5 localized edits or diffs), exact model/snapshot/source/cost, correction counts and remaining limitations. Include no invented examples marked as results. It can be an explicitly failing baseline; only Task 14 supplies final acceptance. Holdout is unopened and untuned.

## Task 13: Finish the collector contract and evidence assertions

**Files**
- Modify: `.superpowers/replay/m2-browser.mjs` and existing private collector assertion tests. `.superpowers/replay/build-audit.sh` was repaired in Task 1b and is not re-owned here.
- Create: `.superpowers/replay/m3-browser.test.mjs`.
- Modify: behavior-renamed `ui/host/src/dashboards/browser-evidence.test.ts` (Task 2), `ui/host/src/dashboards/chart-measurements-dev.ts` only if collector coverage needs DEV-only observable geometry, `docs/benchmarks/2026-10-agent-dashboards-m3.md` evidence methodology.

Budget: $0. No browser launch by Codex, no paid calls. Controller owns the actual run in Task 14. Collector stays private; production does not acquire a browser dependency or audit API.

- [ ] **Step 1: Fail incomplete evidence in assertions**

Tasks 6, 8, 9 and 10 already own adding their collector checks; verify they are present without reimplementing them. Task 13 adds only chat parity/trace/filter-drill collection and cross-cutting assertion helpers. The complete required matrix preserves the existing22: `chat_panel_parity`, `chat_trace`, `chat_filter_drill`, `build_receipt`, `receipt_corrections`, `edit_chips`, `narration`, `request_provenance`, `version_history`, `restore_version`, `fullscreen_panel`, `explain_panel`, `keyboard_point`, `keyboard_range`, `dashboard_shortcuts`, `empty_multiselect`, `pattern_context`. Every required check records both themes/surface, expected vs actual, timestamp, request count/body-scope where applicable, screenshot/private artifact and passed boolean. Syntax/mock evidence cannot satisfy actual-render checks.

Complete evidence assertion helper to use in the private test:

```js
export function missingEvidence(rows,checks,themes=['light','dark']) {
  return checks.flatMap(name=>themes.filter(theme=>!rows.some(r=>r.name===name&&r.theme===theme&&r.passed===true&&r.observed===true)).map(theme=>`${name}:${theme}`));
}
```

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import {missingEvidence} from './m2-browser.mjs';
test('a fixture, missing theme or measured failure cannot become a pass',()=>{
  assert.deepEqual(missingEvidence([{name:'chat_panel_parity',theme:'light',passed:true,observed:true}],['chat_panel_parity']),['chat_panel_parity:dark']);
  assert.equal(missingEvidence([{name:'chat_panel_parity',theme:'light',passed:true,observed:false}],['chat_panel_parity']).length,2);
  assert.equal(missingEvidence([{name:'chat_panel_parity',theme:'light',passed:false,observed:true}],['chat_panel_parity']).length,2);
});
```

No import-time browser launch; preserve current CLI main guard. Tests cover argument validation, bounded waiting, partial output on failure and no accidental success from exit0/file-written. Geometry assertions use browser DOM/native ECharts evidence, not hand-authored passing JSON. Hands-on Chrome evidence may resolve an automation limitation only with an explicit observation/proof; a measured product defect remains failed until fixed and rerun.

- [ ] **Step 2: Extend real interaction collection**

Keep M2's15 types ×2 themes, worst-first palette/escaping/legend/resize checks, partial/stale/empty/error, linked crosshair/brush URL, waterfall/logs, cancellation, annotations/split and S8. Add actual MCP-app attachment from a standard seeded transcript: iframe has generic panels resource, same colors/labels/worst-first/escaped text, worker-driven >60-node service map with no external fetch, drill scope correct and cancellation. Chat parity checks compare the same spec/results in host and iframe, not two different data windows. Scope bridge tool requests and record host network errors/CSP violations.

Reuse Tasks 6–10’s receipt/history/full-screen/Explain/keyboard collection with shared assertion helpers here; seed receipt/history mutations in an owned disposable board via API, then verify through UI; do not bypass the UI action under test. For real agent run evidence, attach Task14's completed run and verify reloaded receipt/apps. Record zero stray narration, exact version/chips, pending/error feedback, full-screen focus/back/query count, saved-dashboard Explain context/answer_only refusal and chat action absence, separate manager fix action, keyboard-selected request matching mouse, range URL and explicit empty-array link. Agent input text containing “Fixing the sort.” as a user request must not be falsely flagged as intermediate narration.

Replace private hard-coded base/module/output locations with environment-supplied `FANOUT_BROWSER_BASE`, `FANOUT_BROWSER_COOKIES`, `FANOUT_BROWSER_OUT`, `FANOUT_BROWSER_MODULE`; validate values at CLI entry. Use repo-relative paths in public docs. Keep audit hooks behind `import.meta.env.DEV` and `__fanout_audit=1`; regular production bundle exposes none. The already-repaired private build-audit script stages a copy, uses host’s one lockfile and Task 1b build recipes, and never resets/checkouts the working tree. If its clean-source rule requires controller commit, hand off to the controller rather than writing git.

```bash
rtk proxy node --check .superpowers/replay/m2-browser.mjs
rtk proxy node --test .superpowers/replay/m3-browser.test.mjs .superpowers/replay/m3-receipts.test.mjs
rtk proxy sh -c 'cd ui/host && bun run test'
rtk proxy sh -c 'cd ui/host && bun run lint'
rtk just ui
rtk just ui-deadcode
```

Report collector's required-check matrix, which tests are fixture assertions, and the exact controller invocation below. No claim that these tests verified browser rendering.

## Task 14: Final measured browser, Chrome and S1–S5 acceptance

**Files**
- Modify: `docs/benchmarks/2026-10-agent-dashboards-m3.md` with final measurements and limitations.
- Private outputs: `.superpowers/replay/` collector evidence/screenshots, `.superpowers/eval/` benchmark aggregates; sealed outputs only under controller-only FANOUT_HOLDOUT_OUT_ROOT outside the worktree, `.superpowers/sdd/2026-10-08-agent-dashboards-m3/task-14-report.md`.
- Product files only if a discovered defect requires a new failing regression and fix; rerun affected checks and refresh the candidate identity afterward. No tuning from holdout results.

Budget: **≤ $5 total** for this task. Plan about $3 for one complete default-model ten-create/five-edit benchmark, with per-call usage metered and per-prompt p95 gate enforced as Task 3 defines. Any paid Explain or sealed holdout run consumes the remaining balance. Browser seeded checks and hands-on Chrome use $0 model spend. Never start another prompt when spent + measured p95 (or $0.60 initially) exceeds $5. No paid judge. If the sealed full set cannot fit the remaining practical prompt gates, record it as unrun for budget reasons; benchmark S1–S5 and the browser matrix are still mandatory, and do not claim holdout acceptance. Controller can ask the user for a separately budgeted sealed run; that is outside this plan's authorized cap.

- [ ] **Step 1: Finish local checks and freeze the candidate**

```bash
rtk just fmt
rtk just check
rtk proxy bun test scripts/dashboard-eval
rtk proxy bun scripts/dashboard-eval/main.ts --mock
rtk proxy node --check .superpowers/replay/m2-browser.mjs
rtk proxy node --test .superpowers/replay/m3-browser.test.mjs .superpowers/replay/m3-receipts.test.mjs
rtk proxy wc -c internal/mcp/apps/*.html
```

Record local output or exact blocked action. `just check` must include boundaries, knip, module-wide Go unused plus pinned deadcode with tests excluded over every cmd/ main, native engine tests, notices, docs and staged UI byte checks. A lint denial is not a pass. The controller reviews/commits and deploys the candidate to a disposable evaluation instance; Codex does not perform git writes or deployment. Record source/build identity, clean-source audit variant and production variant. Verify the two build variants differ only by DEV audit instrumentation, and repeat critical smoke/chat checks on production. Do not publish DEV hooks.

- [ ] **Step 2: Controller runs the collector in both themes and both surfaces**

Controller, using Task13 environment variables:

```bash
rtk proxy node .superpowers/replay/m2-browser.mjs --theme both --deploys --headed --classic-scrollbars --audit
```

Run the whole required matrix; `--only` is for diagnosis, never final acceptance. Verify15×2 visualization rows, all M2 checks and all M3 checks with actual observed evidence. Review every failed/inconclusive row and screenshot. M2 acceptance contained automation limitations; do not copy its counts or turn its notes into M3 passes. Assert S8: each completed dashboard refresh emits exactly one panel request and one annotation request, three completed cycles per theme, total frame cells≤200000. UI interactions that intentionally change range/variables have separately accounted requests. Descriptive render/query timings are recorded; S6/S7 and full S13 capability-board rebuild stay M4.

Report before→after **each** app raw/gzip size. Author-measured baseline (gzip level9, mtime0):

| App | Raw bytes | Gzip bytes |
|---|---:|---:|
| logs.html | 1,633,982 | 555,267 |
| overview.html | 1,058,218 | 363,476 |
| performance.html | 1,642,622 | 559,337 |
| topology.html | 1,672,174 | 567,567 |
| trace.html | 1,069,822 | 367,229 |
| Total | 7,076,818 | 2,412,876 |

After: actual panels.html size and retired five outputs absent, measured with the same gzip settings. If a separate trace app became necessary, record actual size and blocker/ruling rather than hiding it. Compare shared styles/labels/legend/drill and no dual-axis latency/error plot. Validate CSP/connect-src none, inline worker only, host bridge authorization, no old payload decoder and no retired routes in production.

- [ ] **Step 3: Controller performs Chrome and Safari/WebKit checks**

In both light and dark, dashboard and chat: controller performs hands-on Chrome checks and repeats sandbox/inline-worker/parity smoke in Safari or Playwright WebKit. Record engine versions, observed blob Worker URL, 61-node and 400-node layout completion, no data: Worker attempt/external assets/CSP errors, and visible layout-error behavior on initialization failure. Codex launches neither browser nor Playwright. Inspect every in-scope panel type; use chart keyboard series/point→Enter filter/drill, explicit range→Back/zoom-out; full-screen→Inspect→Escape/focus return; saved-dashboard Explain and manager-only Ask Fanout to fix it as distinct actions; chat has neither; history select→restore→new version/author/message; expand receipt and validation corrections→edit chips→reload; click provenance to the request; copy URL with explicit empty multi-select and reopen. Include narrow viewport, large-worker service map, long/HTML-like labels and empty/error/truncated results. Record observed behavior, screenshots and console/CSP errors. Chrome checks are performed by the controller under the no-browser-tools rule for Codex.

Explain must submit pinned observed bounds/vars and an answer-only request; use fake-provider seeded submission capture for zero-cost functional proof. If a real Explain run is used, include its spend in the task ledger and assert answer_only=true/no new dashboard version. Separately capture manager fix-request edit intent and confirm viewer/chat fragments hide that action. Check default model IDs from actual server configuration, not labels typed into the harness.

- [ ] **Step 4: Controller runs a fresh complete benchmark and optional frozen-candidate holdout**

```bash
rtk proxy bun scripts/dashboard-eval/main.ts --base "$FANOUT_EVAL_BASE" --cookies "$FANOUT_EVAL_COOKIES" --model-label claude-sonnet-5-5 --prompts-file "$FANOUT_BENCHMARK_PROMPTS" --edits-file "$FANOUT_EDIT_EXPECTATIONS" --snapshot-manifest "$FANOUT_SNAPSHOT_MANIFEST" --cost-ledger "$FANOUT_EVAL_COST_LEDGER" --budget-usd 5 --out .superpowers/eval/m3-final --set benchmark
```

Before this fresh Task 14 run, replay the same immutable source near now again using Task 12’s procedure, and record shift_ns/source hash/time bounds; all 15 prompts start within <=60 minutes of replay. No clock override.

Required outcomes: S1 **10/10 saved**, S2 **100% returning rows or explained empty at save**, S3 **0 final validation failures**, S4 **median≤45s**, S5 **5/5 consecutive localized edits**. Report every prompt's elapsed save time, panel/check totals and all five diffs, actual provider/model, token/cost accounting and snapshot/source identity. Truncated/incomplete runs fail conclusiveness; do not combine favorable prompts across different candidates into a “10/10”.

After candidate freeze and only if sealed prompts fit the **same remaining $5 ledger** under Task 3’s before-each-prompt p95 gate, the controller verifies sha256 prefix `a74ce656679ca792` without displaying contents, then runs the same CLI with `--prompts-file "$FANOUT_HOLDOUT_PROMPTS" --set holdout --holdout-sha a74ce656679ca792` and `--out "$FANOUT_HOLDOUT_OUT_ROOT/m3-final"`, a controller-only directory outside `.superpowers/eval` and the worktree. Store prompt ids and hashes only; omit prompt/response/tool-input text. Subsequent Codex dispatches never receive access to this directory. Never copy holdout prompts into git, pass them to Codex, inspect individual failures for tuning, or rerun tuned candidates on that holdout. A mismatch blocks opening it. Public report contains aggregate holdout status/cost only or explicitly “unrun, remaining budget insufficient”. Ten-prompt benchmark gates still use the benchmark set, not a smaller favorable holdout subset.

- [ ] **Step 5: Publish the evidence and hand off**

Final benchmark doc states measured PASS/FAIL/INCOMPLETE for every gate, app before/after sizes, total model spend, browser matrix including chat both themes, controller Chrome observations, test/guard output and exact remaining limitations. Link only public repo-relative documents; private artifacts are described by repo-relative paths without secrets/telemetry. Carry forward qualified-log SQL and M4 performance/parity work; external/manual boards and deleted source threads intentionally have no inferred chat provenance. If a product gate fails, keep status FAILED/BLOCKED and name the next fix; do not close M3 merely because the plan tasks were attempted. Final report tells the controller which files changed, each step/outcome, deviations, tests and verification. Controller owns the commit/release decision.
