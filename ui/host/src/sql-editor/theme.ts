import { HighlightStyle } from "@codemirror/language";
import { EditorView } from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { fonts } from "../../../tokens";

export const sqlTheme = EditorView.theme({
  "&": { color: "var(--mantine-color-text)", backgroundColor: "var(--mantine-color-body)", border: "1px solid var(--mantine-color-default-border)", borderRadius: "var(--mantine-radius-sm)" },
  "&.cm-focused": { outline: "2px solid var(--mantine-color-iris-filled)", outlineOffset: "2px" },
  ".cm-scroller": { fontFamily: fonts.display, fontSize: "13px", lineHeight: "1.6", minHeight: "160px", maxHeight: "40vh", overflow: "auto" },
  ".cm-content": { padding: "10px 0", caretColor: "var(--mantine-color-text)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--mantine-color-text)" },
  ".cm-gutters": { backgroundColor: "var(--mantine-color-default)", color: "var(--mantine-color-dimmed)", borderColor: "var(--mantine-color-default-border)", borderRadius: "var(--mantine-radius-sm) 0 0 var(--mantine-radius-sm)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection": { backgroundColor: "var(--mantine-color-iris-light-hover)" },
  ".cm-activeLine": { backgroundColor: "var(--mantine-color-iris-light)" },
  // CodeMirror's own placeholder grey is 3.5:1 on white; comments' grey clears AA on both lines.
  ".cm-placeholder": { color: "var(--mantine-color-gray-text)" },
  ".cm-tooltip": { color: "var(--mantine-color-text)", backgroundColor: "var(--mantine-color-body)", border: "1px solid var(--mantine-color-default-border)", borderRadius: "var(--mantine-radius-sm)" },
  ".cm-tooltip-autocomplete > ul > li[aria-selected]": { color: "var(--mantine-color-iris-light-color)", backgroundColor: "var(--mantine-color-iris-light-hover)" },
  ".cm-diagnostic-error": { borderLeftColor: "var(--mantine-color-bad-text)" },
  ".cm-lintRange-error": { backgroundImage: "none", textDecoration: "underline wavy var(--mantine-color-bad-text)", textUnderlineOffset: "3px" },
});

export const sqlHighlight = HighlightStyle.define([
  { tag: tags.keyword, color: "var(--mantine-color-text)", fontWeight: "600" },
  // These foregrounds also clear AA on the tinted active-line background.
  { tag: [tags.string, tags.number, tags.bool], color: "var(--mantine-color-info-light-color)" },
  { tag: tags.comment, color: "var(--mantine-color-gray-text)", fontStyle: "italic" },
  { tag: [tags.typeName, tags.standard(tags.name)], color: "var(--mantine-color-text)" },
]);
