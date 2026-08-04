// Package brand contains static Fanout brand assets shared by user-facing
// surfaces that cannot render the React brand components.
package brand

// MarkSVG is the server-rendered Fanout mark. Keep its ribbon paths and
// gradients in sync with ui/host/public/favicon.svg, the canonical asset.
// testdata/favicon.svg vendors a copy so the test in this package can assert
// they have not drifted apart.
const MarkSVG = `<svg viewBox="35 44 200 200" width="100%" height="100%" aria-hidden="true">
<defs>
<linearGradient id="fanout-top" x1="54" y1="52" x2="210" y2="104" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="#5FE8CE"/><stop offset=".55" stop-color="#81E4B9"/><stop offset="1" stop-color="#D9F276"/></linearGradient>
<linearGradient id="fanout-mid" x1="58" y1="112" x2="176" y2="154" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="#536FFF"/><stop offset=".52" stop-color="#41B6F8"/><stop offset="1" stop-color="#66D0EE"/></linearGradient>
<linearGradient id="fanout-bot" x1="58" y1="166" x2="145" y2="220" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="#725BFF"/><stop offset=".52" stop-color="#9A50F4"/><stop offset="1" stop-color="#CB55E8"/></linearGradient>
</defs>
<path d="M58 116V88C58 67 75 52 96 52H191C204 52 212 61 212 72C212 84 203 94 191 94H101C82 94 67 102 58 116Z" fill="url(#fanout-top)"/>
<path d="M58 170V139C58 120 72 107 91 107H162C174 107 182 115 182 126C182 137 174 145 162 145H99C79 145 66 154 58 170Z" fill="url(#fanout-mid)"/>
<path d="M58 219V188C58 170 71 157 89 157H126C138 157 146 165 146 176C146 187 138 195 126 195H100C89 195 84 200 84 211C84 225 74 235 61 235H58Z" fill="url(#fanout-bot)"/>
</svg>`

// EmailMarkHTML approximates the flowing-F ribbons with email-safe table
// markup. Inline SVG is intentionally avoided because major mail clients strip
// or fail to render it.
const EmailMarkHTML = `<table role="presentation" aria-hidden="true" cellpadding="0" cellspacing="0" style="border-collapse:separate;width:38px">
<tr><td style="height:8px;width:38px;border-radius:8px;background:#81E4B9;font-size:0;line-height:0">&nbsp;</td></tr>
<tr><td style="height:5px;font-size:0;line-height:0">&nbsp;</td></tr>
<tr><td style="height:8px;width:29px;border-radius:8px;background:#41B6F8;font-size:0;line-height:0">&nbsp;</td></tr>
<tr><td style="height:5px;font-size:0;line-height:0">&nbsp;</td></tr>
<tr><td style="height:8px;width:20px;border-radius:8px;background:#9A50F4;font-size:0;line-height:0">&nbsp;</td></tr>
</table>`
