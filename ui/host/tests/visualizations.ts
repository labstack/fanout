import type {Viz} from "../../panels/types";
export const visualizations=["stat", "gauge", "timeseries", "bar", "table", "text", "heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"] as const satisfies readonly Viz[];
