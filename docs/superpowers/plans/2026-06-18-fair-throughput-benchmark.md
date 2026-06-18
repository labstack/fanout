# Fair Throughput Benchmark Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `scripts/bench-throughput.sh` — a two-VM, SLO-gated harness that measures fanout's ingest **ceiling** and defensible **rated capacity** in achieved rows/s.

**Architecture:** A bash orchestrator provisions a Hetzner private network + two VMs (a `cpx32` fanout-under-test and a larger `cpx41` load driver), ships the current git HEAD to both, boots fanout with prod-default config, ramps `loadgen` through rate steps with an SLO gate to find the ceiling, then certifies a rated capacity with a 15-min sustained soak. All cloud resources are torn down on any exit. Reuses existing `cmd/loadgen` flags — **no Go changes**.

**Tech Stack:** Bash, `hcloud` CLI, SSH/scp, Go (built remotely for `cmd/fanout` + `cmd/loadgen`), fanout's Prometheus `/-/metrics` endpoint.

## Global Constraints

- **No Go changes.** `cmd/loadgen` and `cmd/fanout` are consumed as-is; the plan only adds a shell script and a `justfile` subcommand.
- **All cloud resources torn down on exit** via a single `trap cleanup EXIT INT TERM` — servers first, then network. On any delete failure, print the resource name loudly.
- **Headline metric is achieved server-side rows/s** (`fanout_ingest_rows_total` delta ÷ wall-clock), never the loadgen target traces/s. Rows-per-trace factor ≈ **4.3** at the default data shape.
- **Inter-VM test traffic uses the private network only** (`:4317` ingest, `:7520` metrics+query); the public IP is for our SSH/control plane.
- **Prod-default fanout config:** `FLUSH_SECONDS=15`, `ROLLUP_EVERY=60`; `DUCKLAKE_MAINTENANCE_EVERY_SECONDS=60` (documented deviation so a 15-min soak can verify compaction). DuckDB memory/threads self-size.
- **Data shape (every loadgen call):** `-services 50 -attr-cardinality 200 -error-rate 0.05 -messaging-ratio 0.15`, logs + metrics on, `-workers 48`.
- **SLO gate (all must hold):** drops == 0 · query P95 < 1500 ms · rollup age < 240s · `lake_partitions` ≤ `PART_CAP` (default 800) · RSS stable, no OOM · zero ERROR/send/query errors · fanout alive + `/healthz` reachable at end. Liveness asserted first.
- **Style:** match the existing `scripts/bench-hetzner.sh` and `scripts/soak.sh` idioms (SSHOPTS array, quoted heredocs for remote exec, `snap()` metric helper). Pass `shellcheck` clean.
- **Defaults:** server location `fsn1`, SSH key `v@labstack.com`, Go `1.26.4`, target type `cpx32`, driver type `cpx41`. All overridable by positional args / env, mirroring `bench-hetzner.sh`.

---

### Task 1: Script skeleton — arg parsing, resource registry, teardown trap

**Files:**
- Create: `scripts/bench-throughput.sh`

**Interfaces:**
- Produces: a runnable skeleton with globals `LOC`, `SSH_KEY`, `GOVER`, `TARGET_TYPE`, `DRIVER_TYPE`, `PART_CAP`; arrays `SERVERS=()` and `NETWORKS=()`; functions `register_server <name>`, `register_network <name>`, `cleanup`. `SSHOPTS=(...)` array and `ssh_to <ip> ...` / `scp_to <ip> <src> <dst>` helpers (bypass known_hosts like bench-hetzner.sh).

- [ ] **Step 1: Write the skeleton**

```bash
#!/usr/bin/env bash
# Fair throughput benchmark: provisions a Hetzner private network + two VMs
# (a cpx32 fanout-under-test and a larger cpx41 load driver), ships the current
# git HEAD, ramps loadgen through rate steps under an SLO gate to find the
# ingest CEILING, then certifies a sustained RATED CAPACITY with a 15-min soak.
# Both numbers are reported in achieved server-side rows/s. All cloud resources
# are deleted on exit (trap), including on failure or Ctrl-C.
#
# Usage:  scripts/bench-throughput.sh [TARGET_TYPE] [DRIVER_TYPE] [SSH_KEY] [LOCATION]
# Example: scripts/bench-throughput.sh cpx32 cpx41 v@labstack.com fsn1
# Env:    PART_CAP (max allowed lake_partitions, default 800)
#
# Requires: hcloud CLI with an authenticated context, an uploaded SSH key whose
# private key is a default identity (or loaded in the agent), and a clean build
# of HEAD (HEAD is shipped via git archive).
set -uo pipefail
cd "$(dirname "$0")/.."

TARGET_TYPE="${1:-cpx32}"
DRIVER_TYPE="${2:-cpx41}"
SSH_KEY="${3:-v@labstack.com}"
LOC="${4:-fsn1}"
GOVER="1.26.4"
PART_CAP="${PART_CAP:-800}"
RUN="fanout-tput-$$"

command -v hcloud >/dev/null || { echo "hcloud CLI required" >&2; exit 1; }

SERVERS=()
NETWORKS=()
register_server()  { SERVERS+=("$1"); }
register_network() { NETWORKS+=("$1"); }

cleanup() {
  for s in "${SERVERS[@]:-}"; do
    [ -n "$s" ] || continue
    echo "── deleting server $s ──"
    hcloud server delete "$s" >/dev/null 2>&1 && echo "✓ $s deleted" || echo "⚠ delete server $s FAILED — remove it manually!"
  done
  for n in "${NETWORKS[@]:-}"; do
    [ -n "$n" ] || continue
    echo "── deleting network $n ──"
    hcloud network delete "$n" >/dev/null 2>&1 && echo "✓ $n deleted" || echo "⚠ delete network $n FAILED — remove it manually!"
  done
}
trap cleanup EXIT INT TERM

SSHOPTS=(-o ConnectTimeout=8 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes)
ssh_to()  { local ip="$1"; shift; ssh "${SSHOPTS[@]}" "root@$ip" "$@"; }
scp_to()  { local ip="$1" src="$2" dst="$3"; scp "${SSHOPTS[@]}" -q "$src" "root@$ip:$dst"; }

echo "throughput-bench run $RUN | target=$TARGET_TYPE driver=$DRIVER_TYPE loc=$LOC part_cap=$PART_CAP"
```

- [ ] **Step 2: Lint and syntax-check**

Run: `shellcheck scripts/bench-throughput.sh && bash -n scripts/bench-throughput.sh`
Expected: no output, exit 0. (If `shellcheck` is absent, `brew install shellcheck` first.)

- [ ] **Step 3: Verify the trap fires on empty registries**

Run: `bash scripts/bench-throughput.sh cpx32 cpx41 v@labstack.com fsn1`
Expected: prints the `throughput-bench run …` line, then exits 0 with no "deleting" lines (empty `SERVERS`/`NETWORKS`), proving the `${SERVERS[@]:-}` guard handles the empty case under `set -u`.

- [ ] **Step 4: Commit**

```bash
git add scripts/bench-throughput.sh
git commit -m "feat(bench): fair-throughput harness skeleton + teardown trap"
```

---

### Task 2: Provision the private network and both VMs

**Files:**
- Modify: `scripts/bench-throughput.sh`

**Interfaces:**
- Consumes: `register_server`, `register_network`, `SSHOPTS`, `ssh_to`, globals from Task 1.
- Produces: `make_network` (creates `$RUN-net`, echoes nothing, registers it); `provision <name> <type>` (creates a server attached to the network, registers it, echoes `"<public_ip> <private_ip>"`); after the provisioning block, globals `TARGET_PUB TARGET_PRIV DRIVER_PUB DRIVER_PRIV` are set and both VMs are SSH-reachable.

- [ ] **Step 1: Add provisioning functions and the provisioning block**

```bash
make_network() {
  hcloud network create --name "$RUN-net" --ip-range 10.10.0.0/16 >/dev/null
  hcloud network add-subnet "$RUN-net" --network-zone eu-central --type cloud --ip-range 10.10.1.0/24 >/dev/null
  register_network "$RUN-net"
}

# provision NAME TYPE → echoes "PUBLIC_IP PRIVATE_IP"
provision() {
  local name="$1" type="$2"
  hcloud server create --name "$name" --type "$type" --image ubuntu-24.04 \
    --location "$LOC" --ssh-key "$SSH_KEY" --network "$RUN-net" \
    --label purpose=fanout-throughput-bench >/dev/null
  register_server "$name"
  local pub priv
  pub=$(hcloud server ip "$name")
  priv=$(hcloud server describe "$name" -o format='{{(index .PrivateNet 0).IP}}')
  echo "$pub $priv"
}

wait_ssh() { local ip="$1"; for _ in $(seq 1 40); do ssh_to "$ip" true 2>/dev/null && return 0; sleep 5; done; return 1; }

echo "── creating private network $RUN-net ──"
make_network
echo "── provisioning target ($TARGET_TYPE) + driver ($DRIVER_TYPE) ──"
read -r TARGET_PUB TARGET_PRIV < <(provision "$RUN-target" "$TARGET_TYPE")
read -r DRIVER_PUB DRIVER_PRIV < <(provision "$RUN-driver" "$DRIVER_TYPE")
echo "  target: pub=$TARGET_PUB priv=$TARGET_PRIV"
echo "  driver: pub=$DRIVER_PUB priv=$DRIVER_PRIV"
wait_ssh "$TARGET_PUB" || { echo "target SSH never came up" >&2; exit 1; }
wait_ssh "$DRIVER_PUB" || { echo "driver SSH never came up" >&2; exit 1; }
echo "✓ both VMs reachable"
```

- [ ] **Step 2: Lint and syntax-check**

Run: `shellcheck scripts/bench-throughput.sh && bash -n scripts/bench-throughput.sh`
Expected: clean. (Note: `<(...)` process substitution requires bash, which the shebang guarantees — `shellcheck` may warn SC2046 elsewhere; resolve any warnings before commit.)

- [ ] **Step 3: Confirm the private-IP query format against hcloud**

Run: `hcloud server describe --help | grep -A2 format`
Expected: confirms `-o format='{{...}}'` Go-template support exists in `hcloud v1.65.0`. If the `PrivateNet` template path differs in this version, adjust to the documented field (verify with a throwaway `hcloud server list -o json | jq '.[0].private_net'` shape) before relying on it.

- [ ] **Step 4: Commit**

```bash
git add scripts/bench-throughput.sh
git commit -m "feat(bench): provision private network + target/driver VMs"
```

---

### Task 3: Toolchain install, ship HEAD, and build (target=fanout, driver=loadgen)

**Files:**
- Modify: `scripts/bench-throughput.sh`

**Interfaces:**
- Consumes: `ssh_to`, `scp_to`, `TARGET_PUB`, `DRIVER_PUB`, `GOVER`.
- Produces: `setup_toolchain <pub_ip>`; `ship_and_build <pub_ip> <target|driver>` (ships `git archive HEAD`, builds `bin/fanout` on a target / `bin/loadgen` on a driver, writes a throwaway `.env` on the target). After this block, `/root/fanout/bin/fanout` exists on the target and `/root/fanout/bin/loadgen` exists on the driver.

- [ ] **Step 1: Add toolchain + ship/build functions and invoke them**

```bash
setup_toolchain() {
  local ip="$1"
  ssh_to "$ip" "GOVER='$GOVER' bash -s" <<'REMOTE'
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null && apt-get install -y -qq build-essential git curl >/dev/null 2>&1
cd /tmp && curl -fsSL "https://go.dev/dl/go${GOVER}.linux-amd64.tar.gz" -o go.tgz
rm -rf /usr/local/go && tar -C /usr/local -xzf go.tgz
echo "  $(/usr/local/go/bin/go version) | $(nproc) cores, $(free -g | awk '/Mem/{print $2}')GB RAM"
REMOTE
}

# ship_and_build PUBLIC_IP ROLE   (ROLE = target | driver)
ship_and_build() {
  local ip="$1" role="$2"
  scp_to "$ip" /tmp/fanout-src.tgz /root/fanout-src.tgz
  ssh_to "$ip" "ROLE='$role' bash -s" <<'REMOTE'
set -e
export PATH=$PATH:/usr/local/go/bin CGO_ENABLED=1
mkdir -p /root/fanout && tar -xzf /root/fanout-src.tgz -C /root/fanout && cd /root/fanout
if [ "$ROLE" = "target" ]; then
  cat > .env <<'ENV'
JWT_SECRET=0123456789abcdef0123456789abcdef
JWT_REFRESH_SECRET=abcdef0123456789abcdef0123456789
SMTP_HOST=localhost
SMTP_USER=x
SMTP_PASS=x
SMTP_FROM=fanout@example.com
AI_API_KEY=dummy
AI_PROVIDER=anthropic
ENV
  go build -o bin/fanout ./cmd/fanout
else
  go build -o bin/loadgen ./cmd/loadgen
fi
echo "  built $ROLE binary"
REMOTE
}

echo "── installing toolchain on both VMs ──"
setup_toolchain "$TARGET_PUB"
setup_toolchain "$DRIVER_PUB"
echo "── shipping HEAD ($(git rev-parse --short HEAD)) + building ──"
git archive --format=tar.gz HEAD -o /tmp/fanout-src.tgz
ship_and_build "$TARGET_PUB" target
ship_and_build "$DRIVER_PUB" driver
rm -f /tmp/fanout-src.tgz
```

- [ ] **Step 2: Lint and syntax-check**

Run: `shellcheck scripts/bench-throughput.sh && bash -n scripts/bench-throughput.sh`
Expected: clean.

- [ ] **Step 3: Verify HEAD archives cleanly (local dry-run, no VM)**

Run: `git archive --format=tar.gz HEAD -o /tmp/fanout-src.tgz && tar tzf /tmp/fanout-src.tgz | grep -E 'cmd/(fanout|loadgen)/' | head && rm -f /tmp/fanout-src.tgz`
Expected: lists `cmd/fanout/...` and `cmd/loadgen/main.go`, proving both build targets ship in the archive.

- [ ] **Step 4: Commit**

```bash
git add scripts/bench-throughput.sh
git commit -m "feat(bench): install toolchain, ship HEAD, build fanout/loadgen per role"
```

---

### Task 4: Boot fanout on the target with prod-default config

**Files:**
- Modify: `scripts/bench-throughput.sh`

**Interfaces:**
- Consumes: `ssh_to`, `TARGET_PUB`, `TARGET_PRIV`.
- Produces: `boot_fanout` (starts fanout on the target bound to all interfaces, prod-default flush/rollup, `PUBLIC_READ=true`, waits for `/healthz` over the private IP); `snap <metric>` (sums a Prometheus metric over its label series from the target's private `:7520/-/metrics`, echoing an integer) — used by all later tasks.

- [ ] **Step 1: Add boot + snap helpers and boot the server**

```bash
# snap METRIC → integer sum across label series, scraped over the private net.
# The curl runs on the driver (double-quoted, remote); the awk runs locally in a
# single-quoted program, so its quotes are NOT escaped.
snap() { ssh_to "$DRIVER_PUB" "curl -s -m3 http://$TARGET_PRIV:7520/-/metrics" | awk -v k="$1" '$0 ~ "^"k {s+=$2} END{printf "%d", s+0}'; }

boot_fanout() {
  ssh_to "$TARGET_PUB" "bash -s" <<'REMOTE'
set -e
cd /root/fanout
set -a; . ./.env; set +a
DATA_DIR=/root/fanout/data PUBLIC_READ=true ENV=development \
  OTLP_GRPC_ADDR=:4317 HTTP_ADDR=:7520 \
  FLUSH_SECONDS=15 ROLLUP_EVERY=60 DUCKLAKE_MAINTENANCE_EVERY_SECONDS=60 \
  nohup ./bin/fanout >/root/fanout/f.log 2>&1 &
echo "started fanout pid $!"
REMOTE
  echo "── waiting for fanout /healthz ──"
  for _ in $(seq 1 40); do
    ssh_to "$DRIVER_PUB" "curl -fsS -m2 http://$TARGET_PRIV:7520/healthz" >/dev/null 2>&1 && { echo "✓ fanout healthy"; return 0; }
    sleep 2
  done
  echo "fanout never became healthy" >&2; ssh_to "$TARGET_PUB" "tail -30 /root/fanout/f.log" >&2; return 1
}

boot_fanout
```

- [ ] **Step 2: Lint and syntax-check**

Run: `shellcheck scripts/bench-throughput.sh && bash -n scripts/bench-throughput.sh`
Expected: clean. Confirm the `snap()` awk escaping (`\"%d\"`) survives the outer double-quoted `ssh_to` string — `bash -n` will catch an unbalanced quote.

- [ ] **Step 3: Verify healthz is reached over the PRIVATE ip**

Inspection check (no VM): confirm both the `boot_fanout` readiness loop and `snap` curl use `$TARGET_PRIV`, not `$TARGET_PUB`, so the SLO path is the private network.
Run: `grep -nE 'curl .*TARGET_(PRIV|PUB):(7520|4317)' scripts/bench-throughput.sh`
Expected: every test-traffic curl uses `TARGET_PRIV`.

- [ ] **Step 4: Commit**

```bash
git add scripts/bench-throughput.sh
git commit -m "feat(bench): boot fanout with prod-default config, private-net healthz"
```

---

### Task 5: `run_step` — drive one rate step, evaluate the SLO gate, classify pass/fail/inconclusive

**Files:**
- Modify: `scripts/bench-throughput.sh`

**Interfaces:**
- Consumes: `ssh_to`, `snap`, `DRIVER_PUB`, `TARGET_PRIV`, `TARGET_PUB`, `PART_CAP`.
- Produces: `ROWS_PER_TRACE=4.3` constant; `run_step <target_traces> <dur_sec>` which runs loadgen on the driver, computes **achieved rows/s** from the `fanout_ingest_rows_total` delta, samples growth invariants, and sets globals `STEP_ACHIEVED_RPS`, `STEP_VERDICT` (`pass|fail|inconclusive`), `STEP_REASON`. The function echoes a one-line summary and returns 0 always (verdict is in the globals, not the exit code).

- [ ] **Step 1: Add the rate-per-trace constant and `run_step`**

```bash
ROWS_PER_TRACE=4.3   # 2 spans + 0.15*2 messaging spans + 1 log + 1 metric, at the default data shape

# run_step TARGET_TRACES DUR_SEC  → sets STEP_ACHIEVED_RPS / STEP_VERDICT / STEP_REASON
run_step() {
  local traces="$1" dur="$2"
  local rows0 rows1 drops0 drops1 t0 t1 part rts age rss errs
  rows0=$(snap fanout_ingest_rows_total); drops0=$(snap fanout_rows_dropped_total); t0=$(date +%s)

  # Driver fires loadgen at the target's PRIVATE ip, query-under-load on, query-p95
  # gate armed. loadgen runs on the driver but its stdout/stderr stream back over
  # SSH — capture to a LOCAL file so we can grep its verdict locally. loadgen
  # exits non-zero on any SLO/error failure (the `|| true` keeps the harness alive
  # so we can classify it ourselves).
  local steplog="/tmp/$RUN-step-$traces.log"
  ssh_to "$DRIVER_PUB" "bash -s" <<REMOTE >"$steplog" 2>&1 || true
cd /root/fanout
./bin/loadgen -endpoint $TARGET_PRIV:4317 -rate $traces -duration ${dur}s -workers 48 \
  -services 50 -attr-cardinality 200 -error-rate 0.05 -messaging-ratio 0.15 \
  -metrics-url http://$TARGET_PRIV:7520/-/metrics \
  -query-url http://$TARGET_PRIV:7520 -query-workers 4 -query-rate 20 \
  -max-query-p95-ms 1500 -report /root/fanout/step-$traces.json
REMOTE

  t1=$(date +%s); rows1=$(snap fanout_ingest_rows_total); drops1=$(snap fanout_rows_dropped_total)
  part=$(snap fanout_lake_partitions)
  rts=$(snap fanout_rollup_last_success_timestamp); age=$(( rts > 0 ? t1 - rts : 999 ))
  rss=$(ssh_to "$TARGET_PUB" "ps -C fanout -o rss= 2>/dev/null | awk '{printf \"%d\", \$1/1024}'")
  errs=$(ssh_to "$TARGET_PUB" "grep -cE 'level\":\"ERROR' /root/fanout/f.log")

  local secs=$(( t1 - t0 )); [ "$secs" -lt 1 ] && secs=1
  STEP_ACHIEVED_RPS=$(( (rows1 - rows0) / secs ))
  local target_rps; target_rps=$(awk -v t="$traces" -v r="$ROWS_PER_TRACE" 'BEGIN{printf "%d", t*r}')

  # loadgen prints "FAIL: ..." to stderr (now in the local steplog) on any SLO
  # breach, send error, or query error — grep the LOCAL capture, not the driver.
  local lg_fail; lg_fail=$(grep -c '^FAIL:' "$steplog" 2>/dev/null || echo 0)

  STEP_VERDICT=pass; STEP_REASON=""
  if [ "$(( drops1 - drops0 ))" -gt 0 ]; then STEP_VERDICT=fail; STEP_REASON="drops=$(( drops1 - drops0 ))"
  elif [ "$lg_fail" -gt 0 ]; then STEP_VERDICT=fail; STEP_REASON="loadgen SLO/errors (see step log)"
  elif [ "$age" -gt 240 ]; then STEP_VERDICT=fail; STEP_REASON="rollup age ${age}s>240s"
  elif [ "${part:-0}" -gt "$PART_CAP" ]; then STEP_VERDICT=fail; STEP_REASON="partitions ${part}>${PART_CAP}"
  elif [ "${errs:-0}" -gt 0 ]; then STEP_VERDICT=fail; STEP_REASON="ERROR logs=${errs}"
  elif [ "$STEP_ACHIEVED_RPS" -lt "$(( target_rps * 95 / 100 ))" ]; then STEP_VERDICT=inconclusive; STEP_REASON="achieved ${STEP_ACHIEVED_RPS}<95% of target ${target_rps} rps — driver may be capped"
  fi
  printf "  step %6d tr/s → achieved %7d rows/s | part=%s ageS=%s rssMB=%s | %s %s\n" \
    "$traces" "$STEP_ACHIEVED_RPS" "$part" "$age" "$rss" "$STEP_VERDICT" "$STEP_REASON"
}
```

- [ ] **Step 2: Lint and syntax-check**

Run: `shellcheck scripts/bench-throughput.sh && bash -n scripts/bench-throughput.sh`
Expected: clean. Watch SC2086 on the integer `$(( ))` math and the nested heredoc escaping (`\$?`, `\"`) — the `<<REMOTE` here is **unquoted** so `$TARGET_PRIV`/`$traces` interpolate locally; verify that is intentional and that `\$?` stays literal for the remote shell.

- [ ] **Step 3: Unit-check the achieved-vs-target math in isolation**

Run:
```bash
bash -c 'ROWS_PER_TRACE=4.3; traces=12000; target=$(awk -v t=$traces -v r=$ROWS_PER_TRACE "BEGIN{printf \"%d\", t*r}"); echo "target_rps=$target 95pct=$(( target*95/100 ))"'
```
Expected: `target_rps=51600 95pct=49020` — confirms the rows/s target derivation and the 95% inconclusive threshold compute correctly.

- [ ] **Step 4: Commit**

```bash
git add scripts/bench-throughput.sh
git commit -m "feat(bench): run_step drives one rate step + classifies SLO verdict"
```

---

### Task 6: Ramp loop — find the ceiling and the last passing step

**Files:**
- Modify: `scripts/bench-throughput.sh`

**Interfaces:**
- Consumes: `run_step` and its output globals.
- Produces: globals `CEILING_RPS` (achieved rows/s of the first `fail` step), `LASTPASS_RPS` (achieved rows/s of the highest `pass` step); appends one line per step to `RESULTS=()` for the summary. Ramp steps: `6000 9000 12000 16000 20000 28000` traces/s, 180s each.

- [ ] **Step 1: Add the ramp loop**

```bash
RAMP_STEPS=(6000 9000 12000 16000 20000 28000)
STEP_DUR=180
RESULTS=()
CEILING_RPS=0
LASTPASS_RPS=0

echo "── RAMP (${STEP_DUR}s/step) ──"
for traces in "${RAMP_STEPS[@]}"; do
  run_step "$traces" "$STEP_DUR"
  RESULTS+=("$traces $STEP_ACHIEVED_RPS $STEP_VERDICT ${STEP_REASON:-ok}")
  case "$STEP_VERDICT" in
    pass)         LASTPASS_RPS=$STEP_ACHIEVED_RPS ;;
    fail)         CEILING_RPS=$STEP_ACHIEVED_RPS; echo "  ceiling found at ${CEILING_RPS} rows/s (${STEP_REASON})"; break ;;
    inconclusive) echo "  ⚠ inconclusive — driver may be the bottleneck; not advancing ceiling" ;;
  esac
done

if [ "$CEILING_RPS" -eq 0 ]; then
  echo "  ⚠ top ramp step still PASSED — true ceiling is above ${LASTPASS_RPS} rows/s; extend RAMP_STEPS upward to bracket it"
fi
if [ "$LASTPASS_RPS" -eq 0 ]; then
  echo "  ⚠ even the first ramp step did not PASS — lower RAMP_STEPS; skipping soak" >&2
fi
```

- [ ] **Step 2: Lint and syntax-check**

Run: `shellcheck scripts/bench-throughput.sh && bash -n scripts/bench-throughput.sh`
Expected: clean.

- [ ] **Step 3: Verify the verdict state-machine with a stubbed run_step**

Run:
```bash
bash -c '
RAMP_STEPS=(6000 9000 12000); STEP_DUR=1; RESULTS=(); CEILING_RPS=0; LASTPASS_RPS=0
i=0; run_step(){ i=$((i+1)); STEP_ACHIEVED_RPS=$((i*1000)); if [ $i -ge 3 ]; then STEP_VERDICT=fail; STEP_REASON="drops"; else STEP_VERDICT=pass; STEP_REASON=""; fi; }
for t in "${RAMP_STEPS[@]}"; do run_step "$t" "$STEP_DUR"; case "$STEP_VERDICT" in pass) LASTPASS_RPS=$STEP_ACHIEVED_RPS;; fail) CEILING_RPS=$STEP_ACHIEVED_RPS; break;; esac; done
echo "lastpass=$LASTPASS_RPS ceiling=$CEILING_RPS"'
```
Expected: `lastpass=2000 ceiling=3000` — confirms last-pass tracks the highest pass and ceiling captures the first fail.

- [ ] **Step 4: Commit**

```bash
git add scripts/bench-throughput.sh
git commit -m "feat(bench): ramp loop finds ceiling + last-passing step"
```

---

### Task 7: Soak certification — sustain ~80% of last-pass for 15 min

**Files:**
- Modify: `scripts/bench-throughput.sh`

**Interfaces:**
- Consumes: `LASTPASS_RPS`, `ROWS_PER_TRACE`, `run_step` (reused at soak duration), its verdict globals.
- Produces: globals `RATED_RPS` (achieved rows/s over the soak if it passes, else 0) and `SOAK_VERDICT`. Soak duration 900s; soak target traces/s = `0.8 * LASTPASS_RPS / ROWS_PER_TRACE`.

- [ ] **Step 1: Add the soak block**

```bash
SOAK_DUR=900
RATED_RPS=0
SOAK_VERDICT=skipped
if [ "$LASTPASS_RPS" -gt 0 ]; then
  soak_traces=$(awk -v rps="$LASTPASS_RPS" -v r="$ROWS_PER_TRACE" 'BEGIN{printf "%d", (rps*0.8)/r}')
  echo "── SOAK (${SOAK_DUR}s @ ~80% of last-pass = ${soak_traces} tr/s) ──"
  run_step "$soak_traces" "$SOAK_DUR"
  SOAK_VERDICT=$STEP_VERDICT
  if [ "$STEP_VERDICT" = pass ]; then RATED_RPS=$STEP_ACHIEVED_RPS; fi
  RESULTS+=("SOAK:$soak_traces $STEP_ACHIEVED_RPS $STEP_VERDICT ${STEP_REASON:-ok}")
  # Final liveness assertion (a mid-soak OOM makes snap read 0 → would look like a pass).
  if ! ssh_to "$DRIVER_PUB" "curl -fsS -m3 http://$TARGET_PRIV:7520/healthz" >/dev/null 2>&1; then
    SOAK_VERDICT=fail; RATED_RPS=0; echo "  ✗ fanout unreachable after soak — rated capacity void"
  fi
fi
```

- [ ] **Step 2: Lint and syntax-check**

Run: `shellcheck scripts/bench-throughput.sh && bash -n scripts/bench-throughput.sh`
Expected: clean.

- [ ] **Step 3: Verify the 80%-of-last-pass traces target math**

Run: `bash -c 'awk -v rps=64000 -v r=4.3 "BEGIN{printf \"soak_traces=%d\n\", (rps*0.8)/r}"'`
Expected: `soak_traces=11906` — i.e. given a 64k rows/s last-pass, the soak drives ~11.9k traces/s (≈ 51k rows/s target, comfortably under the ceiling).

- [ ] **Step 4: Commit**

```bash
git add scripts/bench-throughput.sh
git commit -m "feat(bench): soak certifies rated capacity at 80% of last-pass"
```

---

### Task 8: Summary report

**Files:**
- Modify: `scripts/bench-throughput.sh`

**Interfaces:**
- Consumes: `RESULTS`, `CEILING_RPS`, `LASTPASS_RPS`, `RATED_RPS`, `SOAK_VERDICT`, `TARGET_TYPE`.
- Produces: a final printed table + headline numbers; pulls the per-step JSON reports off the target for the record.

- [ ] **Step 1: Add the summary block (end of script, before normal exit)**

```bash
echo
echo "════════ THROUGHPUT — ${TARGET_TYPE} (HEAD $(git rev-parse --short HEAD)) ════════"
printf "%-14s %14s %-12s %s\n" "target tr/s" "achieved rows/s" "verdict" "reason"
for row in "${RESULTS[@]}"; do
  # shellcheck disable=SC2086
  set -- $row
  printf "%-14s %14s %-12s %s\n" "$1" "$2" "$3" "${*:4}"
done
echo "────────────────────────────────────────────────────────────"
echo "ceiling        : ${CEILING_RPS} rows/s (first SLO break)"
echo "rated capacity : ${RATED_RPS} rows/s (sustained ${SOAK_DUR}s, all SLOs held: ${SOAK_VERDICT})"
if [ "$RATED_RPS" -gt 0 ] && [ "$CEILING_RPS" -gt 0 ]; then
  awk -v c="$CEILING_RPS" -v r="$RATED_RPS" 'BEGIN{printf "headroom       : %.2fx (ceiling / rated)\n", c/r}'
fi
echo "════════════════════════════════════════════════════════════"

mkdir -p ./bench-results
ssh_to "$TARGET_PUB" "cd /root/fanout && tar czf - step-*.json 2>/dev/null" > "./bench-results/$RUN.tgz" || true
echo "per-step JSON reports: ./bench-results/$RUN.tgz"
# trap deletes both VMs + the network
```

- [ ] **Step 2: Lint and syntax-check**

Run: `shellcheck scripts/bench-throughput.sh && bash -n scripts/bench-throughput.sh`
Expected: clean (the `SC2086` on `set -- $row` word-splitting is intentional and disabled inline).

- [ ] **Step 3: Verify the table renderer with stub data**

Run:
```bash
bash -c '
RESULTS=("6000 26000 pass ok" "12000 51000 pass ok" "16000 64000 fail drops=12" "SOAK:9000 41000 pass ok")
for row in "${RESULTS[@]}"; do set -- $row; printf "%-14s %14s %-12s %s\n" "$1" "$2" "$3" "${*:4}"; done'
```
Expected: a 4-row aligned table, with the `SOAK:9000` label and `drops=12` reason rendered intact.

- [ ] **Step 4: Add `bench-results/` to .gitignore and commit**

```bash
grep -qxF 'bench-results/' .gitignore || echo 'bench-results/' >> .gitignore
git add scripts/bench-throughput.sh .gitignore
git commit -m "feat(bench): summary table with ceiling, rated capacity, headroom"
```

---

### Task 9: Wire `just stress throughput` and document usage

**Files:**
- Modify: `justfile` (the `stress` recipe dispatcher)
- Modify: `scripts/bench-throughput.sh` (top-of-file usage comment already added in Task 1 — confirm accuracy)

**Interfaces:**
- Consumes: `scripts/bench-throughput.sh`.
- Produces: `just stress throughput [args]` routing to the harness, and a `fair` line in the `just stress` help text.

- [ ] **Step 1: Add the `fair` case and help line to the stress dispatcher**

In `justfile`, inside the `stress` recipe `case "$sub" in` block, add a case beside the existing `hetzner)` line:

```bash
      throughput) exec ./scripts/bench-throughput.sh "$@" ;;
```

And add to the help heredoc (beside the `hetzner` help line):

```bash
        echo "  fair    [target driver]    two-VM SLO-gated ceiling + rated capacity (rows/s)"
```

- [ ] **Step 2: Verify just parses and dispatches**

Run: `just stress` (no args)
Expected: the help text now lists the `fair` subcommand alongside `local/soak/hetzner/profile/drive/watch`.

Run: `just stress throughput --help 2>&1 | head -1` — if the script has no `--help`, expect it to begin provisioning; instead verify routing without spending money:
Run: `grep -n 'fair)' justfile`
Expected: shows the `throughput) exec ./scripts/bench-throughput.sh "$@"` line.

- [ ] **Step 3: Commit**

```bash
git add justfile scripts/bench-throughput.sh
git commit -m "feat(bench): wire 'just stress throughput' + help text"
```

---

### Task 10: Live end-to-end run (USER-GATED — spends money)

**Files:** none (operational).

**Interfaces:** consumes the finished harness.

> **Gate:** This task provisions two real Hetzner VMs + a network and runs ~50 minutes (6 ramp steps × 3 min + 15-min soak + provisioning/build). Do NOT run without explicit user go-ahead. The trap deletes all resources on exit.

- [ ] **Step 1: Pre-flight checks**

Run: `hcloud context active && hcloud ssh-key list | grep -q v@labstack.com && echo ok && grep -q ENCRYPTED ~/.ssh/id_rsa && echo "KEY ENCRYPTED — load into agent" || echo "key usable"`
Expected: `ok` and `key usable` (matches the verified setup; if the key were encrypted, `ssh-add ~/.ssh/id_rsa` first).

- [ ] **Step 2: Launch in the background and watch**

Run: `just stress throughput 2>&1 | tee bench-results/run-$(date +%s).log` (run in background per the harness's long duration)
Expected: provisioning lines → toolchain → build → per-step ramp lines → ceiling → soak → summary table → teardown (`✓ … deleted` for both servers and the network).

- [ ] **Step 3: Confirm no orphaned resources**

Run: `hcloud server list -l purpose=fanout-throughput-bench && hcloud network list | grep fanout-tput || echo "clean"`
Expected: no servers, no network — `clean`.

- [ ] **Step 4: Record the result**

Update `docs/superpowers/specs/2026-06-18-fair-throughput-benchmark-design.md` with a short "Results (YYYY-MM-DD, HEAD <sha>)" section: ceiling rows/s, rated capacity rows/s, headroom, and how they compare to the ~64K burst anchor. Commit.

```bash
git add docs/superpowers/specs/2026-06-18-fair-throughput-benchmark-design.md
git commit -m "docs(bench): record fair throughput results for <sha>"
```

---

## Notes for the implementer

- **Nested heredoc quoting is the main hazard.** `<<'REMOTE'` (quoted) runs `$(...)`/`$var` on the **remote** VM; `<<REMOTE` (unquoted, as in `run_step`) interpolates **locally** before sending. Task 5 deliberately mixes both — keep `\$?`/`\"`/`\$1` escaped where they must reach the remote shell. `bash -n` after every edit catches unbalanced quotes.
- **`set -u` + empty arrays:** always expand registries as `"${ARR[@]:-}"` (Task 1) — a bare `"${ARR[@]}"` under `set -u` errors when empty, which would break the trap on an early failure.
- **The driver, not your laptop, scrapes metrics.** `snap()` and all health checks run `curl` *through the driver* over the private IP, because the private network is only reachable from inside the Hetzner network — your workstation cannot reach `10.10.x.x`.
- **Cost control:** every `hcloud` resource is registered the instant it's created (`register_server`/`register_network`) so the trap can delete partial provisions. If you add a resource, register it immediately.
