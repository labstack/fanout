import { Check, Palette, X } from "@phosphor-icons/react";
import { createContext, ReactNode, useContext, useEffect, useLayoutEffect, useRef, useState } from "react";

export type ThemeID = "verdant" | "midnight" | "ember" | "orchid" | "graphite";
export type MarkID = "monogram" | "signal" | "orbit" | "layers";

type ThemeOption = { id: ThemeID; name: string; description: string; accent: string; canvas: string; contrast: string };
type MarkOption = { id: MarkID; name: string };
type Appearance = { theme: ThemeID; mark: MarkID; setTheme: (theme: ThemeID) => void; setMark: (mark: MarkID) => void };

const appearanceKey = "fanout.appearance";
export const themes: ThemeOption[] = [
  { id: "verdant", name: "Verdant", description: "Calm signal green", accent: "#a7f06a", canvas: "#090b0a", contrast: "#10150c" },
  { id: "midnight", name: "Midnight", description: "Electric cobalt", accent: "#78a9ff", canvas: "#080b12", contrast: "#07101e" },
  { id: "ember", name: "Ember", description: "Warm incident amber", accent: "#ffb45f", canvas: "#0d0a08", contrast: "#1b1006" },
  { id: "orchid", name: "Orchid", description: "Focused ultraviolet", accent: "#c5a3ff", canvas: "#0b0910", contrast: "#150d22" },
  { id: "graphite", name: "Graphite", description: "Neutral and precise", accent: "#d3d9d5", canvas: "#090a0a", contrast: "#101211" },
];
export const marks: MarkOption[] = [
  { id: "monogram", name: "Monogram" },
  { id: "signal", name: "Signal" },
  { id: "orbit", name: "Orbit" },
  { id: "layers", name: "Layers" },
];

function initialAppearance(): { theme: ThemeID; mark: MarkID } {
  try {
    const saved = JSON.parse(localStorage.getItem(appearanceKey) ?? "{}") as Partial<Appearance>;
    return {
      theme: themes.some((item) => item.id === saved.theme) ? saved.theme as ThemeID : "verdant",
      mark: marks.some((item) => item.id === saved.mark) ? saved.mark as MarkID : "signal",
    };
  } catch { return { theme: "verdant", mark: "signal" }; }
}

const initial = initialAppearance();
document.documentElement.dataset.theme = initial.theme;

const AppearanceContext = createContext<Appearance | null>(null);

export function AppearanceProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<ThemeID>(initial.theme);
  const [mark, setMark] = useState<MarkID>(initial.mark);
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.dataset.mark = mark;
    localStorage.setItem(appearanceKey, JSON.stringify({ theme, mark }));
    updateBrowserChrome(theme, mark);
  }, [theme, mark]);
  return <AppearanceContext.Provider value={{ theme, mark, setTheme, setMark }}>{children}</AppearanceContext.Provider>;
}

export function useAppearance() {
  const context = useContext(AppearanceContext);
  if (!context) throw new Error("AppearanceProvider is required");
  return context;
}

export function BrandMark({ size = "regular", mark: override }: { size?: "small" | "regular" | "large"; mark?: MarkID }) {
  const appearance = useAppearance();
  return <span className={`brand-mark ${size}`} aria-hidden="true"><BrandGlyph mark={override ?? appearance.mark} /></span>;
}

export function BrandGlyph({ mark }: { mark: MarkID }) {
  if (mark === "monogram") return <span className="brand-monogram">F</span>;
  if (mark === "signal") return <svg viewBox="0 0 24 24" fill="none"><path d="M6 18V8m0 4h6m0 0V5m0 7h6v7" /><circle cx="6" cy="6" r="2" /><circle cx="12" cy="3" r="2" /><circle cx="18" cy="21" r="2" /></svg>;
  if (mark === "orbit") return <svg viewBox="0 0 24 24" fill="none"><ellipse cx="12" cy="12" rx="9" ry="4.5" /><ellipse cx="12" cy="12" rx="4.5" ry="9" transform="rotate(38 12 12)" /><circle cx="12" cy="12" r="2" fill="currentColor" /></svg>;
  return <svg viewBox="0 0 24 24" fill="none"><path d="m4 8 8-4 8 4-8 4-8-4Z" /><path d="m4 12 8 4 8-4M4 16l8 4 8-4" /></svg>;
}

export function AppearanceMenu() {
  const { theme, mark, setTheme, setMark } = useAppearance();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const closeOutside = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false); };
    const closeEscape = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeEscape);
    return () => { document.removeEventListener("pointerdown", closeOutside); document.removeEventListener("keydown", closeEscape); };
  }, [open]);
  return <div className="relative" ref={root}>
    <button type="button" className="ghost appearance-trigger" aria-label="Appearance" aria-expanded={open} onClick={() => setOpen(!open)}><Palette size={16} weight="bold" aria-hidden="true" /><span className="action-label">Appearance</span></button>
    {open && <section className="fixed inset-x-3 top-[76px] z-50 max-h-[calc(100dvh-88px)] overflow-y-auto rounded-2xl border border-line bg-panel/95 p-4 text-text shadow-2xl backdrop-blur-xl sm:absolute sm:inset-x-auto sm:right-0 sm:top-[calc(100%+12px)] sm:w-[min(380px,calc(100vw-24px))]" aria-label="Appearance settings">
      <div className="mb-4 flex items-start justify-between gap-4"><div><p className="m-0 text-[11px] font-extrabold uppercase tracking-[.16em] text-accent">Appearance</p><p className="mt-1 mb-0 text-sm text-muted">Choose the atmosphere and product mark.</p></div><button className="grid size-8 place-items-center rounded-lg border border-transparent bg-transparent text-muted transition hover:border-line-strong hover:bg-panel-raised hover:text-text" onClick={() => setOpen(false)} aria-label="Close appearance settings"><X size={15} weight="bold" /></button></div>
      <fieldset className="m-0 border-0 p-0"><legend className="mb-2 text-[10px] font-bold uppercase tracking-[.14em] text-muted">Theme</legend><div className="grid grid-cols-2 gap-2">
        {themes.map((option) => <button type="button" key={option.id} className={`group relative min-h-20 rounded-xl border p-3 text-left transition ${theme === option.id ? "border-accent bg-accent/10" : "border-line bg-panel-soft hover:border-line-strong hover:bg-panel-raised"}`} onClick={() => setTheme(option.id)}><span className="mb-2 flex gap-1.5"><i className="size-3 rounded-full" style={{ background: option.canvas }} /><i className="size-3 rounded-full" style={{ background: option.accent }} /></span><strong className="block text-xs text-text">{option.name}</strong><small className="mt-0.5 block text-[10px] text-muted">{option.description}</small>{theme === option.id && <Check className="absolute top-3 right-3 text-accent" size={14} weight="bold" />}</button>)}
      </div></fieldset>
      <fieldset className="mt-4 border-0 p-0"><legend className="mb-2 text-[10px] font-bold uppercase tracking-[.14em] text-muted">Product mark</legend><div className="grid grid-cols-4 gap-2">
        {marks.map((option) => <button type="button" key={option.id} className={`relative grid min-h-17 place-items-center rounded-xl border transition ${mark === option.id ? "border-accent bg-accent/10 text-accent" : "border-line bg-panel-soft text-muted hover:border-line-strong hover:bg-panel-raised hover:text-text"}`} onClick={() => setMark(option.id)} aria-label={option.name}><span className="mark-preview"><BrandGlyph mark={option.id} /></span><small className="text-[9px] font-semibold">{option.name}</small>{mark === option.id && <Check className="absolute top-1.5 right-1.5 text-accent" size={11} weight="bold" />}</button>)}
      </div></fieldset>
    </section>}
  </div>;
}

function updateBrowserChrome(themeID: ThemeID, mark: MarkID) {
  const theme = themes.find((item) => item.id === themeID) ?? themes[0];
  let favicon = document.querySelector<HTMLLinkElement>('link[data-fanout-favicon]');
  if (!favicon) { favicon = document.createElement("link"); favicon.rel = "icon"; favicon.type = "image/svg+xml"; favicon.dataset.fanoutFavicon = "true"; document.head.appendChild(favicon); }
  favicon.href = `data:image/svg+xml,${encodeURIComponent(faviconSVG(mark, theme.accent, theme.contrast))}`;
  document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute("content", theme.canvas);
}

function faviconSVG(mark: MarkID, accent: string, contrast: string) {
  const common = `stroke="${contrast}" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"`;
  const glyph = mark === "monogram"
    ? `<text x="16" y="22" text-anchor="middle" font-family="Arial,sans-serif" font-size="19" font-weight="900" fill="${contrast}">F</text>`
    : mark === "signal"
      ? `<g ${common}><path d="M8 24V11m0 6h8m0 0V8m0 9h8v8"/><circle cx="8" cy="8" r="2.3" fill="${contrast}"/><circle cx="16" cy="5" r="2.3" fill="${contrast}"/><circle cx="24" cy="27" r="2.3" fill="${contrast}"/></g>`
      : mark === "orbit"
        ? `<g ${common} fill="none"><ellipse cx="16" cy="16" rx="11" ry="5.5"/><ellipse cx="16" cy="16" rx="5.5" ry="11" transform="rotate(38 16 16)"/><circle cx="16" cy="16" r="2.2" fill="${contrast}" stroke="none"/></g>`
        : `<g ${common} fill="none"><path d="m6 11 10-5 10 5-10 5-10-5Z"/><path d="m6 16 10 5 10-5M6 21l10 5 10-5"/></g>`;
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="8" fill="${accent}"/>${glyph}</svg>`;
}
