import { Text } from "@mantine/core";
import { useEffect, useState } from "react";
import type { DashboardTime } from "../../panels/types";
import { exactTimestamp } from "../../format";

export function LiveDataMeta({ time, updatedAt }: { time?: DashboardTime; updatedAt?: number | null }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => { const timer = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(timer); }, []);
  const window = time?.from && time.to ? `${exactTimestamp(time.from)} – ${exactTimestamp(time.to)}` : time ? `Last ${time.range ?? "1h"}` : "Refresh every 30s";
  return <Text ff="monospace" size="xs" c="dimmed">{window} · {updatedAt ? `Updated ${Math.max(0, Math.floor((now - updatedAt) / 1000))}s ago` : "Waiting for data"}</Text>;
}
