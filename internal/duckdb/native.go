// Package duckdb registers the extensions compiled into Fanout's pinned
// DuckDB engine. Building requires scripts/with-duckdb.sh; there is one engine.
package duckdb

/*
#include "duckdb.h"
#include "duckdb_static_extension.h"
extern int32_t duckdb_extension_core_functions_describe(duckdb_extension_descriptor *);
extern int32_t duckdb_extension_json_describe(duckdb_extension_descriptor *);
extern int32_t duckdb_extension_parquet_describe(duckdb_extension_descriptor *);
extern int32_t duckdb_extension_icu_describe(duckdb_extension_descriptor *);
static int register_extensions(void) {
 return duckdb_register_static_extension(duckdb_extension_core_functions_describe)
   | duckdb_register_static_extension(duckdb_extension_json_describe)
   | duckdb_register_static_extension(duckdb_extension_parquet_describe)
   | duckdb_register_static_extension(duckdb_extension_icu_describe);
}
*/
import "C"

const Version = "v2.0.0-alpha43763"
const SourceCommit = "96063b9e39749cc0f087bb11501d7edbee527957"

func init() {
	if C.GoString(C.duckdb_library_version()) != Version {
		panic("Fanout requires DuckDB " + Version + "; build with scripts/with-duckdb.sh")
	}
	if C.register_extensions() != 0 {
		panic("DuckDB static extension registration failed")
	}
}
