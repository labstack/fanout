// The GitHub social preview card.
//
// Render it with `just social-card`, which pins the typst version, requires
// Inter on a font path, and fails when the face is missing. Running typst by
// hand does not: it warns about an unknown font family, exits 0, and writes a
// card set in whatever it found instead. The recipe also passes `--root .`,
// without which typst refuses to read the mark below — it sits outside this
// file's directory, and typst sandboxes an input to its own root.
//
// The page is 960pt x 480pt, which is exactly 1280 x 640 pixels at 96 ppi: the
// size GitHub expects for a social preview, and large enough that a link unfurl
// does not resample it.
//
// The mark is read from ui/host/public/favicon.svg — the canonical asset that
// internal/brand tracks — rather than copied here, so a revised mark reaches
// the card the next time it is rendered.
#set page(width: 960pt, height: 480pt, margin: (x: 60pt, top: 44pt, bottom: 34pt), fill: rgb("#0b0f14"))
#set text(font: "Inter", fill: rgb("#f2f5f8"))

#place(top + left, dx: -60pt, dy: -44pt, rect(width: 960pt, height: 5pt,
  fill: gradient.linear(rgb("#5FE8CE"), rgb("#41B6F8"), rgb("#9A50F4"))))

#grid(
  columns: (86pt, 1fr),
  column-gutter: 16pt,
  align: horizon,
  image("../../ui/host/public/favicon.svg", width: 84pt),
  text(size: 56pt, weight: 700, "Fanout"),
)

#v(30pt)
#text(size: 34pt, weight: 700, fill: rgb("#5FE8CE"))[
  Single-binary, agent-native \
  OpenTelemetry investigation.
]

#v(18pt)
#text(size: 20pt, fill: rgb("#8a94a6"))[
  Ingest OTLP, store as Parquet, query with DuckDB, \
  and ask an agent about it. One Go process.
]

// The footer is pushed down by flexible space rather than placed at the bottom
// out of flow. Placed, it drew at a fixed offset regardless of how tall the
// text above had grown, so one extra headline line put the divider through the
// body copy and typst still exited 0. In flow, the same overflow spills onto a
// second page, and a two-page render fails the PNG export outright.
#v(1fr)
#line(length: 100%, stroke: 0.75pt + rgb("#1b2430"))
#v(12pt)
#grid(columns: (1fr, auto),
  text(size: 19pt, fill: rgb("#8a94a6"))[No collector fleet. No object store. One data directory.],
  text(size: 19pt, fill: rgb("#8a94a6"))[github.com/labstack/fanout],
)
