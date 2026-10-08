import { expect, it, vi } from "vitest";
import { observeContentSize } from "./size-reporter";

it("reports intrinsic wrapper pixels once, ignoring repeated and sub-2px changes for three seconds", () => {
  vi.useFakeTimers();
  let resize = () => {};
  const observe = vi.fn();
  vi.stubGlobal("ResizeObserver", class { constructor(callback: () => void) { resize = callback; } observe = observe; disconnect = vi.fn(); });
  const wrapper = document.createElement("div");
  let height = 470.03;
  wrapper.getBoundingClientRect = () => ({ height } as DOMRect);
  document.documentElement.getBoundingClientRect = () => ({ height: 2000 } as DOMRect);
  const send = vi.fn();
  const stop = observeContentSize(wrapper, send);
  expect(observe).toHaveBeenCalledWith(wrapper);
  expect(send).toHaveBeenLastCalledWith({ height: 470 });
  for (height of [470.06, 470.99, 471.4, 471.99, 470.03]) resize();
  vi.advanceTimersByTime(3000);
  expect(send).toHaveBeenCalledTimes(1);
  height = 472.1; resize();
  expect(send).toHaveBeenLastCalledWith({ height: 472 });
  stop(); vi.useRealTimers(); vi.unstubAllGlobals();
});
