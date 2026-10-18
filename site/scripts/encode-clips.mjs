import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { mkdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { clipFilter, clipSegments, freezeDetectFilter, parseFreezeDetect } from "./clip-timeline.mjs";

const input = resolve(process.argv[2] ?? fileURLToPath(new URL("../../ui/host/test-results/landing-recordings/", import.meta.url)));
const output = resolve(process.argv[3] ?? fileURLToPath(new URL("../src/assets/landing/", import.meta.url)));
const temporary = mkdtempSync(join(tmpdir(), "fanout-clips-"));
function run(command, args, capture = false) {
  const result = spawnSync(command, args, { encoding: "utf8", maxBuffer: 16 * 1024 * 1024,
    stdio: capture === "stderr" ? ["ignore", "ignore", "pipe"] : capture ? ["ignore", "pipe", "inherit"] : "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} exited with status ${result.status}${capture === "stderr" ? `\n${result.stderr}` : ""}`);
  return capture === "stderr" ? result.stderr : result.stdout;
}

try {
  await mkdir(output, { recursive: true });
  // Encode every recording present, so a single retake needs no other takes.
  const names = ["chat", "dashboard"].flatMap(view => ["light", "dark"].map(theme => `${view}-${theme}`))
    .filter(name => existsSync(join(input, `${name}.json`)));
  if (!names.length) throw new Error(`No recordings found in ${input}`);
  for (const name of names) {
    const timeline = JSON.parse(readFileSync(join(input, `${name}.json`), "utf8"));
    if (timeline.version !== 1 || timeline.raw_file !== `${name}.webm` || basename(timeline.raw_file) !== timeline.raw_file) {
      throw new Error(`Invalid recording timeline for ${name}`);
    }
    const raw = join(input, timeline.raw_file);
    const probe = JSON.parse(run("ffprobe", ["-v", "error", "-show_entries", "format=duration:stream=width,height", "-of", "json", raw], true));
    if (!probe.streams.some(stream => stream.width === 1280 && stream.height === 800)) throw new Error(`${name} must be recorded at 1280 × 800`);
    const duration = Number(probe.format.duration);
    // Decode the raw video, before trimming, so detection and timeline use the
    // same clock. Info-level stderr carries freeze_start/end (including EOF).
    const detection = run("ffmpeg", ["-hide_banner", "-nostdin", "-nostats", "-loglevel", "info", "-i", raw,
      "-map", "0:v:0", "-an", "-vf", freezeDetectFilter, "-f", "null", "-"], "stderr");
    const freezes = parseFreezeDetect(detection, duration);
    const segments = clipSegments(timeline.waits, duration, timeline.start, freezes);
    const seconds = segments.reduce((sum, segment) => sum + segment.seconds, 0);
    // Reserve 5% of a decimal 4 MB target for muxing; two passes share this budget.
    const bitrate = String(Math.floor(4_000_000 * 8 * 0.95 / seconds));
    const filter = clipFilter(segments);
    for (const format of ["webm", "mp4"]) {
      const passlog = join(temporary, `${name}-${format}`);
      const codec = format === "webm"
        ? ["-c:v", "libvpx-vp9", "-deadline", "good", "-cpu-used", "2", "-row-mt", "1"]
        : ["-c:v", "libx264", "-preset", "slow"];
      const args = ["-hide_banner", "-loglevel", "warning", "-y", "-i", raw,
        "-filter_complex", filter, "-map", "[out]", "-an", ...codec, "-b:v", bitrate, "-passlogfile", passlog];
      run("ffmpeg", [...args, "-pass", "1", "-f", "null", "-"]);
      run("ffmpeg", [...args, "-pass", "2", ...(format === "mp4" ? ["-movflags", "+faststart"] : []), join(output, `${name}.${format}`)]);
    }
    // Reverse only the final second so the PNG is the last encoded frame.
    run("ffmpeg", ["-hide_banner", "-loglevel", "warning", "-y", "-sseof", "-1", "-i", join(output, `${name}.mp4`),
      "-vf", "reverse", "-frames:v", "1", "-update", "1", join(output, `${name}.png`)]);
    console.log(`${name}: ${seconds.toFixed(1)} s, ${freezes.length} frozen intervals, 1280 × 800, 30 fps, approximately 4 MB per format`);
  }
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
