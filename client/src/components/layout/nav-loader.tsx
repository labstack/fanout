import { clsx } from "clsx";
import { useChatStore } from "@/stores/chat";

// Fade-out is driven by `.nav-loader { transition: opacity 0.2s }` in index.css,
// so the component just needs to toggle the `.active` class.
export function NavLoader() {
  const streaming = useChatStore((s) => s.streaming);
  return (
    <div className={clsx("nav-loader", streaming && "active")}>
      <div className="nav-loader-bar" />
    </div>
  );
}
