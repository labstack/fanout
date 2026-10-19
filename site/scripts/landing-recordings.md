# Landing recordings

Record the chat and dashboard slots from an OpenTelemetry Demo instance with
chat enabled.

Expected assets (all at 1280 × 800):

- `site/src/assets/landing/chat-{light,dark}.{webm,mp4,png}`
- `site/src/assets/landing/dashboard-{light,dark}.{webm,mp4,png}`

Encoded videos stay local and are ignored by Git and Docker; PNG posters stay
in the repository so Astro can optimize them. Published videos live in the
Cloudflare R2 bucket `fanout-assets`, served at `https://assets.fanout.run/`.

Each slot stays poster-only until both themes have both video formats in
`site/src/data/landing-media.json`. An absent manifest also shows posters. The
component follows the site theme, pauses off screen and in hidden tabs, and
shows only the poster when reduced motion is enabled. Its `spedUp` prop marks
recordings with shortened waits in the caption.

Set `FANOUT_LANDING_RECORDINGS=1`, `FANOUT_LANDING_BASE_URL` to the existing
instance URL, and `FANOUT_LANDING_SESSION_COOKIE` to a `name=value` session cookie
in the process environment. Run from `ui/host`:

```sh
bun run e2e e2e/specs/app/landing-recordings.spec.ts --project=app-1440-light --project=app-1440-dark
```

Recordings use a 1280 × 800 viewport and raw video at the same size; Playwright
video captures CSS pixels, so a higher device scale only pads the frame. This
only uses the external instance. It types the configured prompts and waits for the
real run and every evidence iframe to load and finish rendering. A take fails
when the run shows a failed step, when a chat run has fewer than four evidence
views or any view without rows, or when the saved dashboard has fewer than six
panels or any non-text panel without rows. Before the
reveal, a ResizeObserver waits for 1.5 seconds without conversation size changes,
including individual evidence and answer blocks, with the final answer rendered.
This settling interval is recorded as a compressible `conversation_ready` wait.
The chat clip then returns the
conversation pane to the user's question and scrolls through the evidence to
the end of the answer over about 8 seconds, followed by a 4-second final dwell.
It measures the `[data-answer-position]` end on every animation frame and keeps
it aligned to the bottom of the conversation viewport throughout the dwell,
retargeting if an evidence view grows. The take fails unless the answer end is
bottom-aligned and its last text line is inside the viewport at the end.
The dashboard clip opens the dashboard from its successful `create_dashboard`
result and records a compressible `dashboard_ready` wait from just before the
Open dashboard click until every saved panel has rendered and the first fold
has settled again. This includes navigation, loading spinners, lazy panel
loading and the setup scroll back to the top. It then scrolls from the top
through all panels to the bottom over about 6 seconds, dwells there for 1.5
seconds, and scrolls back to the top over 1.5 seconds before its 3-second final
dwell. The dashboard poster therefore shows the top of the dashboard.

Scrolls use awaited `requestAnimationFrame` animation with brief easing at the
ends and a steady pace through the middle. Downward durations scale with the
scroll distance: two viewport heights take the baseline 8 or 6 seconds, clamped
between that baseline and 12 seconds. Aim for encoded clips of 20–35 seconds;
review the actual duration after encoding, since real answer and panel content
vary. It creates real chats and dashboards;
no data is mocked or intercepted.

Use a dedicated recording account: before each take, the spec deletes all of
that account’s chats and non-default dashboards.

Raw WebM files and matching JSON timelines go to
`ui/host/test-results/landing-recordings/`, excluded from Git and Docker inputs.
Keep a copy before another Playwright run clears `test-results`. Timelines use
seconds from page creation, with server event timestamps anchored to receipt of
the run's response headers to avoid server clock skew. They list model-thinking
and tool-call intervals from the unmodified AG-UI stream. The `waits` array also
includes readiness waits and a `kind: "hold"` interval covering the entire result
scroll and final dwell, including the dashboard's bottom dwell and return to the
top. Timelines contain no session
credentials or tool result payloads. Raw recordings can contain instance data.

From the repository root, with ffmpeg and ffprobe installed:

```sh
bash site/scripts/encode-clips.sh
# Or use a saved raw output directory:
bash site/scripts/encode-clips.sh /path/to/landing-recordings
```

It encodes every recording present in the input directory. An optional second
directory argument writes encoded assets elsewhere for review.

Before building segments, the encoder decodes the full raw video through
ffmpeg's `freezedetect=n=0.003:d=1.2`. The normalized full-frame noise tolerance
of `0.003` (about −50.5 dB on ffmpeg's scale) ignores small cursor blinks,
typing-caret flicker, and small streamed text or build-stage updates; the
minimum duration of 1.2 s catches near-static stretches without shortening
every brief pause. Detection runs at the raw frame rate and resolution, with
no resampling or downscaling. Its stderr timestamps share the raw timeline's
clock; an unfinished freeze at EOF ends at the ffprobe duration.

The encoder merges overlapping or touching frozen intervals and timeline waits,
then subtracts protected intervals. Every remaining portion longer than 0.6 s
becomes exactly 0.6 s; shorter portions keep their original duration. Holds
preserve the entire scroll and final dwell at real speed, even if detection
marks them frozen. A hold splits a merged interval into separate compressible
portions, each capped at 0.6 s. Typing is protected from the clip start through
the first timeline wait, which starts after submission in these recordings.
An explicit `kind: "typing"` interval is also protected and replaces that
inference; without either an explicit typing interval or a wait boundary, the
opening is conservatively retained in full. These protections also override
timeline waits. The encoder reports the detected interval count and planned
duration for each clip.

It writes VP9 WebM and H.264
MP4 at 1280 × 800 and 30 fps with a two-pass target of about 4 MB per file.
It extracts each PNG poster from the final encoded frame. Review the clips and
posters, then upload from the repository root with Wrangler logged in to the
account that owns `fanout-assets`:

```sh
bash site/scripts/upload-clips.sh
# Or upload an encoded directory used for review:
bash site/scripts/upload-clips.sh /path/to/encoded-clips
```

The uploader writes content-addressed immutable keys, skips unchanged clips
already listed in the manifest, and preserves URLs for clips absent from the
input directory. It never deletes old objects because published pages may still
reference them. After uploading, commit `site/src/data/landing-media.json` and
the PNG posters. If encoding elsewhere, copy the reviewed PNGs into
`site/src/assets/landing/` first. Never commit WebM or MP4 files.
