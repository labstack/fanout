// Node >= 22 defines an experimental globalThis.localStorage getter that returns undefined
// (no --localstorage-file) and shadows happy-dom's Storage; replace it with a real one.
if (!globalThis.localStorage) {
  const { Storage } = window as unknown as { Storage: new () => Storage };
  Object.defineProperty(globalThis, "localStorage", { value: new Storage(), configurable: true });
}

// happy-dom has no FontFaceSet, so document.fonts is undefined. Mantine's
// autosize textarea subscribes to its "loadingdone" event, which throws on
// mount without this; an EventTarget that never fires is enough.
if (!document.fonts) {
  Object.defineProperty(document, "fonts", { value: new EventTarget(), configurable: true });
}
