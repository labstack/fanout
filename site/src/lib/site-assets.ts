/** Absolute public asset URLs require the configured canonical site origin. */
export function siteAssetURL(site: URL | undefined, path: string): string {
  if (!site) throw new Error("Fanout site URL is required");
  return new URL(path, site).href;
}
