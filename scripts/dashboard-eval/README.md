# Dashboard evaluation CLI

Run from the repository root with the installed Bun and Git. There are no added
packages, provider credentials, paid judges, or embedded benchmark prompts.

```sh
rtk proxy bun test scripts/dashboard-eval
rtk proxy bun scripts/dashboard-eval/main.ts --mock
rtk proxy bun scripts/dashboard-eval/main.ts --help
```

`just script-tests`, included in `just check`, runs both the script tests and
the mock CLI. The mock uses ephemeral loopback `Bun.serve` HTTP only, with no
authentication or provider calls. It executes ten simulated saves and five
consecutive edits through the real transport/scorer, then checks nonzero exit
codes and specific S flags for twenty-three injected failures. Its $0 and synthetic snapshot are fixture
evidence of scoring, not evidence of model quality or real telemetry replay.
`just check` uses `--mock --no-output`, leaving no artifacts. An explicit
mock invocation without that flag uses a fresh `.superpowers/eval/mock-<uuid>/mock/summary.json`;
`--out` and `--model-label` can select another fresh destination.

## Controller inputs

Real evaluation is a separate, explicitly budgeted controller operation. Task 3
does not run it. Keep all input paths private and environment supplied:

```sh
rtk proxy bun scripts/dashboard-eval/main.ts \
  --base "$FANOUT_EVAL_BASE" --cookies "$FANOUT_EVAL_COOKIES" \
  --model-label "$FANOUT_EVAL_LABEL" --out "$FANOUT_EVAL_OUT" \
  --prompts-file "$FANOUT_EVAL_PROMPTS" --set benchmark \
  --budget-usd "$FANOUT_EVAL_CAP" --cost-ledger "$FANOUT_EVAL_LEDGER" \
  --snapshot-manifest "$FANOUT_EVAL_SNAPSHOT" --edits-file "$FANOUT_EVAL_EDITS"
```

Options also include `--help`, `--mock`, `--no-output`, `--holdout-sha` and the
settlement operation below. A model label is a model name or `provider:model`;
each component uses at most 80 ASCII letters/digits/dots/underscores/dashes,
begins with a letter or digit, and cannot contain `..`. The label never selects
the server's model. A bare model name uses the server's configured provider;
without a label, the first server `model_configuration` fixes the expected
default for the entire evaluation. Every metered create/edit call must match.
Unknown, mixed or mismatched identity records `model_mismatch`, exits 2 and
sets every S flag false. Old servers without configuration evidence cannot
prove an unlabeled run. `default` is the output directory label in that case.
Origins reject credentials, paths, queries and fragments. Redirects are refused.
Cookies follow Netscape expiry, domain, path, Secure and HttpOnly rules and are
never copied into output, logs or committed fixtures.

The prompt file is a JSON array of exactly ten unique `{id, prompt}` objects.
The controller supplies the ten private benchmark prompts from the spec.
The edit file has five operations in exactly this order: `title`, `threshold`,
`add`, `remove`, `unit`. It is prepared for the first successfully saved board.
Title/threshold/unit operations identify a real panel with `panel_id` or
`panel_index`; the CLI resolves its actual saved ID before submitting each turn.
`value` supplies the exact title, canonical thresholds array (including `status`
from `ok`, `warn`, `bad`) or unit; unit operations accept only `field: "unit"`.
Add supplies an authored `panel` with a unique ID and optional `after` naming
an existing panel; omission appends. Remove names that newly added ID. Pick compatible
values and types; a percentile remains a percentile. A no-op is rejected.
Expected full specs and private edit prompts are constructed before submission.

The immutable snapshot manifest supplies `source_hash` (SHA-256), signed decimal
nanosecond strings `start_ns`, `end_ns`, `shift_ns`, and an absolute UTC
`replayed_at`. One manifest covers the ten creates and five edits. The controller
must replay the immutable source near the run's current time and verify these
bounds against the instance. At startup, the CLI requires its start time to be
inside the bounds (with a five-minute tolerance) and within five minutes of
`replayed_at`; historical replays fail before HTTP. It records the exact bytes/hash
and does not retime product data. It records absolute observation/start/finish
timestamps as well. The candidate source hash covers Git HEAD and the current
candidate files, including uncommitted files, under the explicit source roots;
private evaluation inputs and files with sealed names are excluded.

## Metering and retry rules

The ledger is a private JSON object with `schema: 1`, `rates`, `calls`, `prompts`.
Start with empty calls/prompts and controller-verified rates for every configured
provider/model. Each rate requires `provider`, `model`, absolute `verified_at`,
boolean `input_includes_cache`, and numeric USD per million tokens in `input`,
`output`, `cache_read`, `cache_write`. OpenAI input includes cached input;
Anthropic's uncached input is separate. No provider price table is bundled.

Runtime `model_call_usage` custom events identify `run_id`, 1-based `step`,
`provider`, observed `model`, `status`, and provider-reported TokenUsage.
The model comes from the provider response when supplied, otherwise its configured
model. A separate `model_configuration` records the configured default.
Snapshots within a call replace earlier counts; one record is emitted per call.
Token fields are `input_tokens`, `output_tokens`, `cache_read_tokens`,
`cache_write_tokens`, `reasoning_tokens`. Reasoning is part of output and is not
charged twice. Failure and incomplete calls retain all reported counts. The
runtime's 16-step limit is unchanged; provider response bodies are not logged.

The CLI debits each correlated call immediately, writes the ledger atomically,
and settles the measured whole-prompt total before the next turn. A ledger lock
prevents concurrent writers. Duplicate identical call records are not charged
again. Failed or truncated prompts with contiguous, fully metered calls settle
as `status: "failed"`, keep their cost, fail S1 and continue under the gate.
Only completed prompts contribute to p95. Conflicting correlation, missing
usage/rates, gaps in steps and unknown settlements stop subsequent HTTP. Reuse the same ledger across
invocations: the cap covers all creates, edits, Explain, targeted reruns and
holdout activity in that task, rather than resetting for each CLI process.

Before every prompt, the gate requires `spent + estimate <= cap`. The estimate
is nearest-rank p95 of completed whole-prompt costs, or exactly **$0.60 before
any measurement**. Equality at the cap is allowed. This practical gate does not
bound the theoretical cost of an in-flight provider call. Missing cost is null,
with the known metered subtotal recorded separately. Only the explicitly local
mock bypasses this gate with synthetic zero-dollar rates. Real-gate tests also
exercise loopback responses without that bypass.

For unknown usage, first reconcile `agui_runs` and correlated usage logs. Then:

```sh
rtk proxy bun scripts/dashboard-eval/main.ts \
  --settle "$FANOUT_EVAL_PROMPT_ID" --cost-usd "$FANOUT_EVAL_VERIFIED_COST" \
  --reason "$FANOUT_EVAL_SETTLEMENT_REASON" --cost-ledger "$FANOUT_EVAL_LEDGER"
```

The ID may be the unique unsettled prompt ID or its run ID. The cost is the
verified **whole-prompt total**, never below already metered spend. This operation
makes no HTTP requests. It appends `manual_settlement` evidence (cost, reason and
absolute time) to the prompt without deleting calls, settles it as failed, and
excludes it from p95. The next artifact includes all manual settlements (sealed
summaries expose only their aggregate count). Repeated settlement is refused.
Reuse this ledger and the same task cap on subsequent invocations.

The exclusive `<ledger>.lock` is removed on normal exit. After a crash, confirm
that no process is using that exact ledger and preserve/reconcile its entries
before removing **only that stale lock**. Never delete or reset the ledger to
resume. Fixed error categories distinguish `ledger_lock`, `ledger`,
`settlement`, `sealed_hash`, `output_root`, `snapshot_manifest`, `options`,
`private_inputs`, `output`, `mock_verification` and `evaluation`; no raw input,
provider body or filesystem path is printed.

Agent POSTs, restore and every mutation are never retried. An idempotent GET
may retry once on transport failure/5xx before consuming a response. Invalid
JSON, consumed streams, aborts and redirects are not retried. Each turn has a
240-second client observation deadline. Post-run versions and panel checks each
have their own 30-second deadline; thread/status reads are independently bounded. Split UTF-8, CR/LF, multiline SSE,
tool IDs/names, terminal RUN_FINISHED/RUN_ERROR and truncation are checked.
After a disconnect the CLI probes persisted run status with an independent
bounded read and stops. The current server has no run-status read endpoint;
an unavailable probe is recorded explicitly. The controller must reconcile
`agui_runs` and the correlated structured usage logs before any manual retry;
the CLI never repeats the agent POST or infers completion from a thread read.

## Scoring and evidence

- **S1:** exactly ten complete runs with immutable successful save observations.
- **S2:** every saved panel ID, including text panels, has exactly one executed
  check. `ok` requires positive rows, or a text panel with nonblank saved `content`; `empty` requires both a nonblank executor
  diagnosis and a nonblank authored description. No unchecked/duplicate evidence.
- **S3:** every save has checked validation, with zero saved invalid specs.
  Only checked, saved invalid specs count as validation failures; unknown validation fails.
- **S4:** every latency is finite/nonnegative; the true median is **<=45000 ms**.
  All ten runs must be complete. Latency runs from POST initiation to the last
  successful immutable save result, the exact version scored by S1–S3.
  `first_save_ms` and `run_end_ms` record the separate first-save and run-end times.
- **S5:** exactly five consecutive edits on one board/thread, each with the next
  version, requested result and exact expected changed IDs. Entire specs,
  metadata, time, annotations, variables and array order are compared. Only
  object key order is normalized. Title/threshold/unit edits preserve grids.
  Add/remove/move may pack physical grids; layout/order is recorded separately
  once. For an added panel only, absent authored `width`, `height`, `reduce` and
  `query.bucket` may acquire the corresponding server defaults. Explicit values,
  every other added field and all neighbors' authored fields remain exact.

Save observations come from mutation results, corroborated by the persisted
version list and tool-message error flags. A later read cannot substitute its
dashboard. The executor receives that immutable saved spec; rows are reduced
to counts/status/diagnosis, never exported as telemetry values. Task 12 must
use the authoritative save/check provenance supplied by Tasks 5–6; warning text
or a successful POST is not validation evidence.

Schema 3 output includes timestamps, label and observed provider/model,
candidate and source/manifest hashes and bounds, immutable save/version/check
evidence, expected/actual authored and physical edit diffs, terminal events,
S1–S5 and ledger cost. Benchmark output must be under `.superpowers/eval/`.
Benchmark inputs also reject the sealed hash prefix, including renamed or hard-linked
copies. Frozen sealed hashes are verified before creating any output.
Input/output paths reject symlinks and outputs cannot overwrite an existing run.
Exits: **0** complete pass, **1** measured failure, **2** invalid, incomplete or
budget-blocked evidence. `--mock` exits 0 only when its positive fixture and all
failure injections behave as expected.

## Sealed set and cleanup

Only the controller's final frozen-candidate invocation may use `--set holdout`
with `--holdout-sha` matching the complete frozen prompt file hash. Dispatches
must never open sealed prompts or use aggregate results to tune the candidate.
Set `FANOUT_HOLDOUT_OUT_ROOT` to a controller-owned location outside the worktree
and `.superpowers/eval/`; the ledger must also be outside the worktree. Use a
fresh label and output directory. Output is whitelisted to aggregate evidence
and prompt IDs/hashes; it omits responses, tool inputs, specs, edit text and
individual failure content. Keep these outputs inaccessible to later dispatches.

The runner does not delete dashboards or threads. Use a disposable owned
instance/account, and let the controller remove only that disposable instance
or its explicitly recorded owned dashboard IDs after preserving evidence.
Never delete a controller's real dashboards or sweep an entire shared account.
Private output directories created by a mock run may be removed by their exact
recorded path; do not glob private evaluation inputs.

## Server response goldens

`internal/agent/dashboard_eval_golden_test.go` seeds native telemetry, saves and
edits through the dashboard service, executes the real panel executor and streams
a fake provider through the real agent SSE encoder. `testdata/server.json` contains
those responses: text `ok` without a frame, saved/added defaults and placement,
incomplete usage and RUN_ERROR. Only volatile IDs, timestamps and execution
milliseconds are normalized. No benchmark prompts, keys or real telemetry are
copied. Score, transport and mock tests consume this same golden.

With the required offline Go environment, `just dashboard-eval-generate` rewrites
it and `just dashboard-eval-generate-check` verifies currentness. The latter is a
fresh Go test (`-count=1`) in `just check`. Truncation comes only from real
`model_call_usage.status: "incomplete"`; RUN_FINISHED has no truncation field.
