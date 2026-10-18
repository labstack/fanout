import { PostgreSQL, SQLDialect, type SQLNamespace } from "@codemirror/lang-sql";
import { completeFromList } from "@codemirror/autocomplete";
import type { TelemetrySchema } from "./schema";

// PostgreSQL's quoting, casts and window syntax are the closest base for
// DuckDB. These additions cover Fanout's panel macros and analytical SQL.
export const duckDB = SQLDialect.define({
  ...PostgreSQL.spec,
  keywords: `${PostgreSQL.spec.keywords} qualify exclude replace pivot unpivot summarize asof positional semi anti union by name columns sample using key`,
  types: `${PostgreSQL.spec.types} variant timestamp_ns timestamptz_ns hugeint uhugeint ubigint uinteger`,
  builtin: `${PostgreSQL.spec.builtin ?? ""} time_bucket epoch_ns epoch_ms make_timestamp_ns try_cast count_if arg_max arg_min any_value quantile_cont quantile_disc approx_quantile histogram first last list list_extract list_value list_distinct list_sort unnest struct_pack struct_extract map map_keys map_extract map_extract_value json_extract json_extract_string json_keys json_type to_json regexp_matches regexp_full_match regexp_replace regexp_extract starts_with contains greatest least isfinite isinf isnan date_diff date_sub`,
  specialVar: "$?",
});

export const macroCompletions = completeFromList([
  { label: "$__window", type: "function", detail: "Time filter", info: "Use $__window(time_column) to apply the dashboard time range." },
  { label: "$__bucket", type: "function", detail: "Time bucket", info: "Use $__bucket(time_column) to group by the panel interval." },
]);

export function completionSchema(schema: TelemetrySchema): SQLNamespace {
  return Object.fromEntries(Object.entries({ ...schema.signals, ...schema.sql_relations }).map(([name, relation]) => [name,
    relation.columns.map(column => ({ label: column.name, type: "property", detail: column.sql_type ?? column.type, info: column.description })),
  ]));
}
