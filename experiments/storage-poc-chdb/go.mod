module github.com/labstack/fanout/experiments/storage-poc-chdb

go 1.27.0

require (
	github.com/chdb-io/chdb-go/lib/embedded v0.260700.1
	github.com/chdb-io/chdb-go/v2 v2.1.0
	github.com/labstack/fanout v0.0.0
)

require (
	github.com/chdb-io/chdb-go/lib/darwin-amd64 v0.260700.1 // indirect
	github.com/chdb-io/chdb-go/lib/darwin-arm64 v0.260700.1 // indirect
	github.com/chdb-io/chdb-go/lib/linux-amd64 v0.260700.1 // indirect
	github.com/chdb-io/chdb-go/lib/linux-arm64 v0.260700.1 // indirect
	github.com/ebitengine/purego v0.8.2 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/labstack/fanout => ../..
