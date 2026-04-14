import { useState, useEffect, useRef } from "react";
import { useNavigation } from "react-router";

export function NavLoader() {
  const navigation = useNavigation();
  const isLoading = navigation.state === "loading";
  const [visible, setVisible] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout>>(null);

  if (isLoading && !visible) {
    setVisible(true);
  }

  useEffect(() => {
    if (!isLoading && visible) {
      timerRef.current = setTimeout(() => setVisible(false), 200);
      return () => {
        if (timerRef.current) clearTimeout(timerRef.current);
      };
    }
  }, [isLoading, visible]);

  return (
    <div className={`nav-loader ${visible ? "active" : ""}`}>
      <div className="nav-loader-bar" />
    </div>
  );
}
