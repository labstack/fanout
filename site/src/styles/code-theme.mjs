// Syntax uses the product surfaces and violet accent. Values carry the accent,
// keys and commands use ink, and structure stays quiet. Health colors retain
// their status meaning rather than becoming decorative syntax colors.

const dark = {
  fg: "#EDEDEF",
  bg: "#121214",
  dim: "#A1A1A9",
  bright: "#EDEDEF",
  accent: "#B5B0FF",
  invalid: "#f26d78",
};

const light = {
  fg: "#53535B",
  bg: "#EFEFF1",
  dim: "#6D6D73",
  bright: "#111113",
  accent: "#5A50D8",
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

export const pitotCodeDark = theme("pitot-dark", "dark", dark);
export const pitotCodeLight = theme("pitot-light", "light", light);
