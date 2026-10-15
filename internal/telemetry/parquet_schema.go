package telemetry

import (
	"reflect"
	"sync"

	"github.com/parquet-go/parquet-go"
)

// Schemas are immutable once built, so one per row type serves every commit,
// compaction and validation instead of reflecting over the struct each time.
var telemetrySchemas sync.Map // reflect.Type -> *parquet.Schema

// Every writer and full-row reader uses this physical schema, including empty
// schema batches and compaction. Dotted OTel names remain literal object keys.
func telemetrySchema[T any]() *parquet.Schema {
	key := reflect.TypeFor[T]()
	if cached, ok := telemetrySchemas.Load(key); ok {
		return cached.(*parquet.Schema)
	}
	schema, _ := telemetrySchemas.LoadOrStore(key, buildTelemetrySchema[T]())
	return schema.(*parquet.Schema)
}

func buildTelemetrySchema[T any]() *parquet.Schema {
	base := parquet.SchemaOf(new(T))
	fields := parquet.Group{}
	for _, field := range base.Fields() {
		switch field.Name() {
		case "attributes":
			fields[field.Name()] = shreddedObject(parquet.Group{
				"messaging.system":           parquet.Optional(parquet.String()),
				"messaging.destination.name": parquet.Optional(parquet.String()),
			})
		case "resource":
			fields[field.Name()] = shreddedObject(parquet.Group{
				"service.name":                parquet.Optional(parquet.String()),
				"service.namespace":           parquet.Optional(parquet.String()),
				"service.version":             parquet.Optional(parquet.String()),
				"deployment.environment.name": parquet.Optional(parquet.String()),
			})
		default:
			fields[field.Name()] = field
		}
	}
	return parquet.NewSchema(base.Name(), fields)
}

func shreddedObject(fields parquet.Group) parquet.Node {
	node, err := parquet.ShreddedVariant(fields)
	if err != nil {
		panic(err)
	} // The fixed schema is a programmer-owned constant.
	return parquet.Optional(node)
}
