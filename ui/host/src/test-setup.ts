// Node >= 22 defines an experimental globalThis.localStorage getter that returns undefined
// (no --localstorage-file) and shadows happy-dom's Storage; replace it with a real one.
if (!globalThis.localStorage) {
  const { Storage } = window as unknown as { Storage: new () => Storage };
  Object.defineProperty(globalThis, "localStorage", { value: new Storage(), configurable: true });
}
