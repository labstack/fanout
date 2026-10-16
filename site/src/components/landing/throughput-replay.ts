import type uPlot from "uplot";

export type Sample = { at_seconds: number; interval_seconds: number; spans: number; logs: number; metrics: number };
const signals = ["spans", "logs", "metrics"] as const;
const WINDOW = 120;
const format = new Intl.NumberFormat("en-US");
export const clock = (seconds: number) => {
  const whole = Math.max(0, Math.floor(seconds));
  return `${String(Math.floor(whole / 60)).padStart(2, "0")}:${String(whole % 60).padStart(2, "0")}`;
};

// Keep measured timestamps and rates intact, including unequal scrape intervals.
// Only the time coordinate is offset on repeat. Retain a point before the left
// edge so the line clips continuously; never fabricate a zero baseline.
export function replayFrame(samples: Sample[], duration: number, elapsed: number) {
  const min = elapsed - WINDOW;
  const points: (Sample & { time: number })[] = [];
  for (let loop = Math.max(0, Math.floor(min / duration) - 1); loop <= Math.floor(elapsed / duration); loop++) {
    for (const sample of samples) {
      const time = loop * duration + sample.at_seconds;
      if (time <= elapsed) points.push({ ...sample, time });
    }
  }
  const first = points.findIndex(point => point.time >= min);
  const visible = points.slice(Math.max(0, (first < 0 ? points.length : first) - 1));
  const current = visible.at(-1)!;
  // Hold the last received value until the next measured sample arrives.
  if (current.time < elapsed) visible.push({ ...current, time: elapsed });
  const data: uPlot.AlignedData = [visible.map(point => point.time), ...signals.map((_, index) =>
    visible.map(point => signals.slice(0, index + 1).reduce((sum, key) => sum + point[key], 0)))];
  return { min, max: elapsed, points: visible, current, data };
}

export function mountReplay(root: HTMLElement, load: () => Promise<typeof uPlot>) {
  const node = <T extends HTMLElement>(selector: string) => root.querySelector<T>(selector)!;
  const host = node("[data-plot]");
  const button = node<HTMLButtonElement>("[data-replay]");
  const elapsedLabel = node("[data-elapsed]");
  const rateLabel = node("[data-rate]");
  const stateLabel = node("[data-state]");
  const tooltip = node("[data-tooltip]");
  const tooltipTime = node("[data-tooltip-time]");
  const hoverValues = signals.map(key => node(`[data-hover=${key}]`));
  const samples: Sample[] = JSON.parse(root.dataset.series!);
  const duration = Number(root.dataset.seconds);
  const ceiling = Number(root.dataset.ceiling);
  const step = Number(root.dataset.step);
  const motion = matchMedia("(prefers-reduced-motion: reduce)");
  const finalElapsed = elapsedLabel.textContent;
  let elapsed = Math.min(samples[0].at_seconds + WINDOW, samples.at(-1)!.at_seconds);
  let frame = replayFrame(samples, duration, elapsed);
  let plot: uPlot | undefined;
  let pending: Promise<void> | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let started = 0;
  let inView = false;
  let paused = false;
  let disposed = false;
  let colors: string[] = [];
  let ink = "", grid = "", font = "";
  const visible = () => !disposed && !motion.matches && inView && !document.hidden;
  const active = () => visible() && !paused;
  const palette = () => {
    const style = getComputedStyle(root);
    colors = signals.map(key => style.getPropertyValue(`--fo-series-${key}`).trim());
    ink = style.getPropertyValue("--sl-color-gray-2").trim();
    grid = style.getPropertyValue("--sl-color-gray-5").trim();
    font = `11px ${style.getPropertyValue("--fo-font-display").trim()}`;
  };
  const readouts = () => {
    elapsedLabel.textContent = `${clock(elapsed % duration)} / ${clock(duration)}`;
    rateLabel.textContent = `· ${format.format(frame.current.spans + frame.current.logs + frame.current.metrics)} items/s, 5 s rate`;
    rateLabel.hidden = false;
    stateLabel.textContent = elapsed >= duration ? `Replay ${Math.floor(elapsed / duration) + 1}` : "Measured stream";
  };
  const hover = (chart: uPlot) => {
    const point = chart.cursor.idx == null ? undefined : frame.points[chart.cursor.idx];
    tooltip.hidden = chart.cursor.left! < 0 || !point;
    if (!tooltip.hidden && point) {
      tooltipTime.textContent = `${clock(point.time % duration)} · items/s, 5 s rate`;
      hoverValues.forEach((value, i) => { value.textContent = format.format(point[signals[i]]); });
    }
  };
  const resize = () => {
    const { width, height } = host.getBoundingClientRect();
    if (plot && width > 0 && height > 0 && (plot.width !== Math.round(width) || plot.height !== Math.round(height)))
      plot.setSize({ width: Math.round(width), height: Math.round(height) });
  };
  const paint = () => {
    frame = replayFrame(samples, duration, elapsed);
    plot?.batch(() => {
      plot!.setData(frame.data, false);
      plot!.setScale("x", { min: frame.min, max: frame.max });
    });
    readouts();
  };
  const stop = () => {
    if (timer !== undefined) {
      elapsed += (performance.now() - started) / 1000;
      clearTimeout(timer);
      timer = undefined;
    }
  };
  const schedule = () => {
    if (!active() || !plot || timer !== undefined) return;
    started = performance.now();
    // Compensate timer drift; hidden/off-screen/user-paused time never counts.
    timer = setTimeout(() => {
      elapsed += (performance.now() - started) / 1000;
      timer = undefined;
      if (active()) { paint(); schedule(); }
    }, 1000);
  };
  const create = async () => {
    const Plot = await load();
    if (!visible() || plot) return;
    palette();
    frame = replayFrame(samples, duration, elapsed);
    const { width, height } = host.getBoundingClientRect();
    plot = new Plot({
      width: Math.round(width), height: Math.round(height),
      legend: { show: false }, select: { show: false, left: 0, top: 0, width: 0, height: 0 },
      cursor: { drag: { x: false, y: false }, points: { show: false } },
      scales: { x: { time: false, auto: false, range: () => [frame.min, frame.max] }, y: { auto: false, range: () => [0, ceiling] } },
      axes: [
        { stroke: () => ink, font, size: 36, space: 70, grid: { show: false }, ticks: { show: false },
          values: (_, ticks) => ticks.map(value => value < 0 ? "" : clock(value)) },
        { stroke: () => ink, font, size: 52, grid: { stroke: () => grid, width: 0.5 }, ticks: { show: false },
          splits: () => Array.from({ length: 4 }, (_, i) => i * step),
          values: (_, ticks) => ticks.map(value => value === 0 ? "0" : value >= 1e6 ? `${value / 1e6}m` : `${value / 1000}k`) },
      ],
      series: [{}, ...signals.map((key, i) => ({ label: key, stroke: () => colors[i], width: 1,
        fill: () => `color-mix(in srgb, ${colors[i]} 12%, transparent)`, points: { show: false } }))],
      bands: [1, 2].map(i => ({ series: [i + 1, i] as [number, number],
        fill: () => `color-mix(in srgb, ${colors[i]} 12%, transparent)` })),
      hooks: {
        setCursor: [hover],
        draw: [chart => {
          // Mark every loop boundary in the window without resetting its history.
          const ctx = chart.ctx;
          ctx.save(); ctx.strokeStyle = grid; ctx.setLineDash([3 * Plot.pxRatio, 4 * Plot.pxRatio]);
          ctx.lineWidth = Plot.pxRatio;
          for (let loop = Math.max(1, Math.ceil(frame.min / duration)); loop * duration <= elapsed; loop++) {
            const x = Math.round(chart.valToPos(loop * duration, "x", true));
            ctx.beginPath(); ctx.moveTo(x, chart.bbox.top); ctx.lineTo(x, chart.bbox.top + chart.bbox.height); ctx.stroke();
          }
          ctx.restore();
        }],
      },
    }, frame.data, host);
    plot.root.setAttribute("aria-hidden", "true");
    root.dataset.mounted = "true";
    button.hidden = false;
    readouts();
  };
  const sync = () => {
    if (!active()) stop();
    if (!visible()) return;
    if (plot) {
      palette(); resize(); plot.redraw(false, true); paint(); schedule();
    } else if (!pending) {
      pending = create().catch(() => { /* The complete SVG remains usable if the module cannot load. */ })
        .finally(() => { pending = undefined; if (plot) schedule(); });
    }
  };
  const toggle = () => {
    paused = !paused;
    button.textContent = paused ? "Play" : "Pause";
    button.setAttribute("aria-label", paused ? "Play measured throughput" : "Pause measured throughput");
    sync();
  };
  const onMotion = () => {
    if (motion.matches) {
      stop(); plot?.destroy(); plot = undefined;
      delete root.dataset.mounted;
      tooltip.hidden = true; button.hidden = true;
      rateLabel.hidden = true; rateLabel.textContent = "";
      elapsedLabel.textContent = finalElapsed; stateLabel.textContent = "Full measured run";
    } else sync();
  };
  const intersection = new IntersectionObserver(entries => { inView = entries.some(entry => entry.isIntersecting); sync(); });
  const size = new ResizeObserver(() => { if (visible()) resize(); });
  const theme = new MutationObserver(() => { if (visible()) { palette(); plot?.redraw(false, true); } });
  intersection.observe(root); size.observe(host);
  theme.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  button.addEventListener("click", toggle);
  motion.addEventListener("change", onMotion);
  document.addEventListener("visibilitychange", sync);
  // A bfcache page pauses and resumes; a departing page releases all resources.
  const onHide = (event: PageTransitionEvent) => { stop(); if (!event.persisted) cleanup(); };
  const cleanup = () => {
    if (disposed) return;
    disposed = true; stop(); plot?.destroy();
    intersection.disconnect(); size.disconnect(); theme.disconnect();
    button.removeEventListener("click", toggle); motion.removeEventListener("change", onMotion);
    document.removeEventListener("visibilitychange", sync);
    document.removeEventListener("astro:before-swap", cleanup);
    window.removeEventListener("pagehide", onHide); window.removeEventListener("pageshow", sync);
  };
  window.addEventListener("pagehide", onHide); window.addEventListener("pageshow", sync);
  document.addEventListener("astro:before-swap", cleanup);
  return cleanup;
}
