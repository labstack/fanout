// Package brand contains static Pitot brand assets shared by user-facing
// surfaces that cannot render the React brand components.
package brand

// MarkSVG is the full Waterfall mark, matching ui/host/public/mark.svg.
// The favicon has a separate pixel-snapped cut for small sizes.
const MarkSVG = `<svg viewBox="0 0 24 24" width="100%" height="100%" aria-hidden="true">
<style>.i{stroke:#111113}.d{stroke:#B4B4BB}.a{stroke:#5A50D8}@media (prefers-color-scheme:dark){.i{stroke:#EDEDEF}.d{stroke:#4A4A52}.a{stroke:#8F8CFF}}</style>
<g fill="none" stroke-width="2.6" stroke-linecap="round"><path class="a" d="M3.3 4.5H20.7"/><path class="i" d="M7 9.5H16.5"/><path class="i" d="M7 14.5H11.5"/><path class="d" d="M10.5 19.5H17"/></g>
</svg>`

// EmailMarkHTML renders Waterfall with email-safe tables in fixed light colors.
// Dimensions are twice the 24-unit mark, including its round cap extents.
const EmailMarkHTML = `<table role="presentation" aria-hidden="true" cellpadding="0" cellspacing="0" width="48" style="border-collapse:separate;width:48px">
<tr><td style="padding:6.4px 0 0 4px"><table role="presentation" cellpadding="0" cellspacing="0" width="40" style="border-collapse:separate;width:40px"><tr><td style="height:5.2px;border-radius:2.6px;background:#5A50D8;font-size:0;line-height:0">&nbsp;</td></tr></table></td></tr>
<tr><td style="padding:4.8px 0 0 11.4px"><table role="presentation" cellpadding="0" cellspacing="0" width="24.2" style="border-collapse:separate;width:24.2px"><tr><td style="height:5.2px;border-radius:2.6px;background:#111113;font-size:0;line-height:0">&nbsp;</td></tr></table></td></tr>
<tr><td style="padding:4.8px 0 0 11.4px"><table role="presentation" cellpadding="0" cellspacing="0" width="14.2" style="border-collapse:separate;width:14.2px"><tr><td style="height:5.2px;border-radius:2.6px;background:#111113;font-size:0;line-height:0">&nbsp;</td></tr></table></td></tr>
<tr><td style="padding:4.8px 0 0 18.4px"><table role="presentation" cellpadding="0" cellspacing="0" width="18.2" style="border-collapse:separate;width:18.2px"><tr><td style="height:5.2px;border-radius:2.6px;background:#B4B4BB;font-size:0;line-height:0">&nbsp;</td></tr></table></td></tr>
</table>`
