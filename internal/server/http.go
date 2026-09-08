// Package server serves Fanout's browser and OTLP surfaces on one listener.
package server

import (
	"crypto/tls"
	"net/http"
	"strings"
	"time"
)

// New keeps ingest outside browser middleware. net/http owns both HTTP/1 and
// HTTP/2 connections, including plaintext HTTP/2, so Shutdown drains them all.
func New(addr string, app, otlp, grpc http.Handler) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Reserve the whole OTLP HTTP namespace, including invalid paths and
			// methods, so they can never fall through to the SPA.
			if r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/") {
				otlp.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
				// ServeHTTP rejects HTTP/1 and unsupported gRPC content types.
				grpc.ServeHTTP(w, r)
				return
			}
			app.ServeHTTP(w, r)
		}),
		Protocols:         protocols,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No global read/write deadline: agent SSE can outlive an export.
		// OTLP/HTTP enforces its own decompressed body limit.
	}
}
