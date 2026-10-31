/** GET /api/telemetry/schema, as read by SQL completion and the Explore builder (also used by MCP). */
export type TelemetrySchema = {
  signals: Record<string, { columns: SchemaColumn[]; attributes?: SchemaAttribute[] }>;
  sql_relations: Record<string, { description?: string; columns: SchemaColumn[] }>;
  services?: { value: string; count?: number }[];
  measures?: string[];
  /** auto, then every bucket FanQL accepts, shortest first. */
  buckets?: string[];
};

type SchemaColumn = { name: string; type: string; description?: string; sql_type?: string };
type SchemaAttribute = { key: string; scope: string; type: string; count?: number };
