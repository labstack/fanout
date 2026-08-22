// Syntax colours for the Ayu Mono theme.
//
// Expressive Code takes its frame from Starlight's grey tokens but its syntax
// palette is independent, so without this the code blocks ship GitHub's blue
// and purple inside an Ayu frame — the only thing on the page speaking a
// different language.
//
// The palette is deliberately narrow, and narrower than Ayu's own. Almost every
// code block on this site is a command line, a YAML document or an environment
// variable, and none of those are improved by twelve token classes. What a
// reader needs to pick out is the value they are meant to change. So values
// carry the accent, the keys and commands naming them sit at full strength, and
// structure and comments fall back.
//
// The accent is `--info` from the product palette rather than a colour invented
// here. The three health hues are load-bearing on this site — green, amber and
// red mean healthy, degraded and unhealthy in the product itself — so tinting a
// string literal green would put "green means healthy" in direct conflict with
// itself. Violet is the one accent in the palette that carries no status.

const dark = {
  fg: "#bfbdb6",
  bg: "#131721",
  dim: "#737681",
  bright: "#fafafa",
  accent: "#d2a6ff",
  invalid: "#f26d78",
};

const light = {
  fg: "#5c6166",
  bg: "#f8f9fa",
  dim: "#8a9199",
  bright: "#171b24",
  accent: "#7c4dcc",
  invalid: "#c4382d",
};

function theme(name, type, c) {
  return {
    name,
    type,
    colors: {
      "editor.background": c.bg,
      "editor.foreground": c.fg,
    },
    settings: [
      {
        scope: ["comment", "punctuation.definition.comment"],
        settings: { foreground: c.dim, fontStyle: "italic" },
      },
      // The part a reader is being shown: values.
      {
        scope: [
          "string",
          "string.quoted",
          "meta.embedded.line",
          "constant.other.symbol",
        ],
        settings: { foreground: c.accent },
      },
      {
        scope: [
          "constant.numeric",
          "constant.language",
          "constant.language.boolean",
        ],
        settings: { foreground: c.accent },
      },
      // Keys, flags and commands read as the thing being described.
      {
        scope: [
          "entity.name.tag",
          "support.type.property-name",
          "meta.object-literal.key",
          "variable.other.readwrite",
        ],
        settings: { foreground: c.bright },
      },
      {
        scope: ["entity.name.function", "support.function"],
        settings: { foreground: c.bright },
      },
      // Structure stays quiet — a terminal does not colour its own punctuation.
      {
        scope: [
          "keyword",
          "storage",
          "storage.type",
          "keyword.operator",
          "punctuation",
        ],
        settings: { foreground: c.fg },
      },
      {
        scope: ["variable.parameter", "entity.name.type", "support.class"],
        settings: { foreground: c.fg },
      },
      {
        scope: ["invalid", "invalid.illegal"],
        settings: { foreground: c.invalid },
      },
    ],
  };
}

export const fanoutCodeDark = theme("fanout-ayu-dark", "dark", dark);
export const fanoutCodeLight = theme("fanout-ayu-light", "light", light);
