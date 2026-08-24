// The GitHub social preview card.
//
// Render it with Inter on a font path. The face is not installed system-wide,
// so without the path the card silently falls back to a substitute and stops
// looking like the product:
//
//   typst compile --font-path <dir-with-Inter> --ppi 96 --format png \
//     docs/media/social-card.typ docs/media/social-card.png
//
// The page is 960pt x 480pt, which is exactly 1280 x 640 pixels at 96 ppi: the
// size GitHub expects for a social preview, and large enough that a link unfurl
// does not resample it.
#set page(width: 960pt, height: 480pt, margin: (x: 60pt, top: 44pt, bottom: 34pt), fill: rgb("#0b0f14"))
#set text(font: "Inter", fill: rgb("#f2f5f8"))

#place(top + left, dx: -60pt, dy: -44pt, rect(width: 960pt, height: 5pt,
  fill: gradient.linear(rgb("#5FE8CE"), rgb("#41B6F8"), rgb("#9A50F4"))))

#grid(
  columns: (86pt, 1fr),
  column-gutter: 16pt,
  align: horizon,
  image("social-card-mark.svg", width: 84pt),
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

#place(bottom + left, dy: 0pt, block(width: 840pt)[
  #line(length: 100%, stroke: 0.75pt + rgb("#1b2430"))
  #v(12pt)
  #grid(columns: (1fr, auto),
    text(size: 19pt, fill: rgb("#8a94a6"))[No collector fleet. No object store. One data directory.],
    text(size: 19pt, fill: rgb("#8a94a6"))[github.com/labstack/fanout],
  )
])
