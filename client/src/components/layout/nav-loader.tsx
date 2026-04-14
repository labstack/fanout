import { useState, useEffect, useRef } from "react";
import { useChatStore } from "@/stores/chat";

export function NavLoader() {
  const streaming = useChatStore((s) => s.streaming);
  const [visible, setVisible] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout>>(null);

  useEffect(() => {
    if (streaming) {
      setVisible(true);
      if (timerRef.current) clearTimeout(timerRef.current);
    } else if (visible) {
      timerRef.current = setTimeout(() => setVisible(false), 200);
      return () => {
        if (timerRef.current) clearTimeout(timerRef.current);
      };
    }
  }, [streaming, visible]);

  return (
    <div className={`nav-loader ${visible ? "active" : ""}`}>
      <div className="nav-loader-bar" />
    </div>
  );
}
