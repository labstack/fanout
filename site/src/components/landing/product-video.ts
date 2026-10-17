/** Templates keep autoplay and downloads inert until motion and visibility allow them. */
export function mountProductVideo(root: HTMLElement) {
  const slots = [...root.querySelectorAll<HTMLElement>("[data-video-theme]")];
  const control = root.querySelector<HTMLButtonElement>("[data-video-control]")!;
  if (!root.querySelector("template")) return () => {};
  const motion = matchMedia("(prefers-reduced-motion: reduce)");
  let visible = false, paused = false, disposed = false;
  const videos = new Map<HTMLElement, HTMLVideoElement>();

  function update() {
    const theme = document.documentElement.dataset.theme === "light" ? "light" : "dark";
    const allowed = !disposed && visible && !document.hidden && !motion.matches;
    control.hidden = !allowed;
    control.textContent = paused ? "Play" : "Pause";
    control.setAttribute("aria-label", paused ? "Play clip" : "Pause clip");
    for (const slot of slots) {
      const active = allowed && !paused && slot.dataset.videoTheme === theme;
      let video = videos.get(slot);
      if (active && !video) {
        const template = slot.querySelector<HTMLTemplateElement>("template")!;
        video = template.content.firstElementChild!.cloneNode(true) as HTMLVideoElement;
        video.muted = true;
        const player = video;
        player.addEventListener("playing", () => {
          if (!disposed && !motion.matches && !player.paused) slot.dataset.playing = "";
        });
        player.addEventListener("error", () => { delete slot.dataset.playing; });
        slot.append(player);
        videos.set(slot, player);
      }
      if (video) {
        if (active) void video.play().catch(error => {
          delete slot.dataset.playing;
          if (!disposed && error instanceof Error && error.name === "NotAllowedError") {
            paused = true;
            update();
          }
        });
        else video.pause();
      }
      if (motion.matches || slot.dataset.videoTheme !== theme) delete slot.dataset.playing;
    }
  }

  const intersection = new IntersectionObserver(entries => {
    visible = entries.some(entry => entry.isIntersecting);
    update();
  });
  const theme = new MutationObserver(update);
  intersection.observe(root);
  theme.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  const toggle = () => { paused = !paused; update(); };
  control.addEventListener("click", toggle);
  motion.addEventListener("change", update);
  document.addEventListener("visibilitychange", update);
  return () => {
    disposed = true;
    update();
    intersection.disconnect();
    theme.disconnect();
    control.removeEventListener("click", toggle);
    motion.removeEventListener("change", update);
    document.removeEventListener("visibilitychange", update);
  };
}
