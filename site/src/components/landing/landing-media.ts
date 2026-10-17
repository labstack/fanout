export type LandingMedia = Record<string, { webm?: string; mp4?: string }>;

/** A slot needs complete sources in both themes before playback is enabled. */
export function hasProductVideo(manifest: LandingMedia, view: "chat" | "dashboard"): boolean {
  return ["light", "dark"].every(theme => {
    const clip = manifest[`${view}-${theme}`];
    return [clip?.webm, clip?.mp4].every(url => typeof url === "string" && url.length > 0);
  });
}
