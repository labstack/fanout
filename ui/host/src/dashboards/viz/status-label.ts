/** Sentence-cases a single status word ("ERROR", "degraded") for a badge.
 * Anything else, such as a unit-formatted value ("1.5 KiB") or a code
 * ("HTTP 503"), is shown as given: case carries meaning there. */
export function statusLabel(text: string): string {
  return text !== "OK" && /^[A-Za-z]+$/.test(text) ? text.charAt(0).toUpperCase() + text.slice(1).toLowerCase() : text;
}
