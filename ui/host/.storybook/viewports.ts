export const reviewWidths = {
  desktop: { width: 1280, height: 800 },
  laptop: { width: 1024, height: 768 },
};

export const viewportOptions = Object.fromEntries(
  Object.entries(reviewWidths).map(([name, { width, height }]) => [name, { name, styles: { width: `${width}px`, height: `${height}px` } }]),
);
