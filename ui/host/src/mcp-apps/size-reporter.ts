/** Observe content, never the viewport: iframe height must not be an input. */
export function observeContentSize(wrapper: HTMLElement, send: (size: { height: number }) => unknown) {
  let last: number | undefined;
  const measure = () => {
    const height = wrapper.getBoundingClientRect().height;
    if (last !== undefined && Math.abs(height - last) < 2) return;
    last = height;
    void send({ height: Math.round(height) });
  };
  const observer = new ResizeObserver(measure);
  observer.observe(wrapper);
  measure();
  return () => observer.disconnect();
}
