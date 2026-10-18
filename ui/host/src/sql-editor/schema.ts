/** The completion subset of GET /api/telemetry/schema (also used by MCP). */
export type TelemetrySchema = {
  signals: Record<string, { columns: SchemaColumn[] }>;
  sql_relations: Record<string, { description?: string; columns: SchemaColumn[] }>;
};

type SchemaColumn = { name: string; type: string; description?: string; sql_type?: string };
