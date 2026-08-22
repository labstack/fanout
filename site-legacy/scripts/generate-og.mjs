// Generates site/public/og.png (1200x630) from an SVG template.
// Wired as a `prebuild` script so the OG image is always fresh on `bun run build`.
//
// Every face here is the display one: a share card is chrome and a wordmark,
// never sustained prose, so the split that governs the site itself resolves to
// mono throughout.
//
// Caveat worth knowing before trusting the output: sharp rasterises SVG text
// with the fonts fontconfig can find on the machine, not the ones this
// repository installs into node_modules. Neither IBM Plex nor the DM Sans and
// Fragment Mono named here before it is present on a stock CI runner, so the
// card has always rendered in a fallback face. The stacks below are therefore
// written to degrade to a generic monospace deliberately rather than to a
// proportional default, which is what the previous sans-led stacks did.

import sharp from "sharp";
import { mkdirSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const outPath = resolve(__dirname, "../public/og.png");
mkdirSync(dirname(outPath), { recursive: true });

const svg = `
<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="630" viewBox="0 0 1200 630">
  <defs>
    <radialGradient id="halo" cx="50%" cy="10%" r="60%">
      <stop offset="0%" stop-color="#60a5fa" stop-opacity="0.22"/>
      <stop offset="70%" stop-color="#09090b" stop-opacity="0"/>
    </radialGradient>
    <radialGradient id="halo2" cx="85%" cy="100%" r="50%">
      <stop offset="0%" stop-color="#a78bfa" stop-opacity="0.14"/>
      <stop offset="70%" stop-color="#09090b" stop-opacity="0"/>
    </radialGradient>
    <linearGradient id="gradText" x1="0" x2="0" y1="0" y2="1">
      <stop offset="0%" stop-color="#fafafa"/>
      <stop offset="100%" stop-color="#a1a1aa"/>
    </linearGradient>
  </defs>

  <!-- bg -->
  <rect width="1200" height="630" fill="#09090b"/>
  <rect width="1200" height="630" fill="url(#halo)"/>
  <rect width="1200" height="630" fill="url(#halo2)"/>

  <!-- grid texture -->
  <g stroke="#ffffff" stroke-opacity="0.02" stroke-width="1">
    <line x1="0" y1="158" x2="1200" y2="158"/>
    <line x1="0" y1="315" x2="1200" y2="315"/>
    <line x1="0" y1="473" x2="1200" y2="473"/>
    <line x1="300" y1="0" x2="300" y2="630"/>
    <line x1="600" y1="0" x2="600" y2="630"/>
    <line x1="900" y1="0" x2="900" y2="630"/>
  </g>

  <!-- brand mark + wordmark -->
  <g transform="translate(80 80)">
    <g stroke="#60a5fa" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" fill="none">
      <path d="M6.9 33.1C-1 25.2 -1 12.8 6.9 4.9"/>
      <path d="M12.8 27.2c-4.6-4.6-4.6-12.2 0-16.9"/>
      <circle cx="19" cy="19" r="3"/>
      <path d="M25.2 10.8c4.6 4.6 4.6 12.2 0 16.9"/>
      <path d="M31.1 4.9C39 12.8 39 25.2 31.1 33.1"/>
    </g>
    <text x="60" y="28" font-family="IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace" font-size="22" font-weight="700" letter-spacing="3" fill="#e4e4e7">FANOUT</text>
  </g>

  <!-- headline -->
  <text x="80" y="310" font-family="IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace" font-size="78" font-weight="700" letter-spacing="0" fill="url(#gradText)">Observability that</text>
  <text x="80" y="400" font-family="IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace" font-size="78" font-weight="700" letter-spacing="0">
    <tspan fill="#60a5fa">runs anywhere</tspan>
    <tspan fill="url(#gradText)"> you can</tspan>
  </text>
  <text x="80" y="490" font-family="IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace" font-size="78" font-weight="700" letter-spacing="0" fill="url(#gradText)">run a binary.</text>

  <!-- tag -->
  <g transform="translate(80 540)">
    <rect x="0" y="0" width="330" height="42" rx="21" fill="#60a5fa" fill-opacity="0.06" stroke="#60a5fa" stroke-opacity="0.3"/>
    <circle cx="22" cy="21" r="5" fill="#34d399"/>
    <text x="38" y="28" font-family="IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace" font-size="16" letter-spacing="2" fill="#60a5fa">SELF-HOSTED · SINGLE BINARY</text>
  </g>

  <!-- footer url -->
  <text x="1120" y="570" text-anchor="end" font-family="IBM Plex Mono, ui-monospace, SFMono-Regular, Menlo, monospace" font-size="18" fill="#71717a">fanout.run</text>
</svg>
`.trim();

try {
  const png = await sharp(Buffer.from(svg)).png().toBuffer();
  writeFileSync(outPath, png);
  console.log(`wrote ${outPath} (${png.length} bytes)`);
} catch (err) {
  console.error(`generate-og: failed to render or write ${outPath}: ${err.message}`);
  process.exit(1);
}
