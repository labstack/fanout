// The GitHub social preview card, which the documentation site also serves as
// its og:image at /social-card.png.
//
// Render it with `just social-card`, which pins the typst version, requires
// Geist on a font path, and fails when the face is missing. Running typst by
// hand does not: it warns about an unknown font family, exits 0, and writes a
// card set in whatever it found instead. The recipe also passes `--root .`,
// without which typst refuses to read the mark below — it sits outside this
// file's directory, and typst sandboxes an input to its own root.
//
// The page is 960pt x 480pt, which is exactly 1280 x 640 pixels at 96 ppi: the
// size GitHub expects for a social preview, and large enough that a link unfurl
// does not resample it.
//
// The fixed dark mark avoids media queries, which Typst cannot resolve.
#set page(width: 960pt, height: 480pt, margin: (x: 60pt, top: 44pt, bottom: 34pt), fill: rgb("#0B0B0C"))
#set text(font: "Geist", fill: rgb("#EDEDEF"))

#grid(
  columns: (86pt, 1fr),
  column-gutter: 16pt,
  align: horizon,
  image("../../ui/host/public/mark-dark.svg", width: 84pt),
  text(size: 56pt, weight: 600, tracking: -0.045em, "pitot"),
)

#v(30pt)
#text(size: 34pt, weight: 700, fill: rgb("#EDEDEF"))[
  Single-binary, agent-native \
  OpenTelemetry investigation.
]

#v(18pt)
#text(size: 20pt, fill: rgb("#A1A1A9"))[
  Ingest OTLP, store as Parquet, query with DuckDB, \
  and ask an agent about it. One Go process.
]

// The footer is pushed down by flexible space rather than placed at the bottom
// out of flow. Placed, it drew at a fixed offset regardless of how tall the
// text above had grown, so one extra headline line put the divider through the
// body copy and typst still exited 0. In flow, the same overflow spills onto a
// second page, and a two-page render fails the PNG export outright.
#v(1fr)
#line(length: 100%, stroke: 0.75pt + rgb("#232327"))
#v(12pt)
#grid(columns: (1fr, auto),
  text(size: 19pt, fill: rgb("#A1A1A9"))[No collector fleet. No object store. One data directory.],
  text(size: 19pt, fill: rgb("#A1A1A9"))[github.com/labstack/pitot],
)
