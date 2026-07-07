// Package pb re-exports the lightweight protobuf helpers used by generated Go clients.
package pb

import serverpb "github.com/demiurgos-hub/golem-engine/golem/pb"

// Reader is a minimal protobuf wire-format decoder.
type Reader = serverpb.Reader

// Writer is a minimal protobuf wire-format encoder.
type Writer = serverpb.Writer

// NewReader creates a Reader over buf.
func NewReader(buf []byte) *Reader { return serverpb.NewReader(buf) }

// Float32 returns a pointer to v.
func Float32(v float32) *float32 { return serverpb.Float32(v) }

// Float64 returns a pointer to v.
func Float64(v float64) *float64 { return serverpb.Float64(v) }

// Int32 returns a pointer to v.
func Int32(v int32) *int32 { return serverpb.Int32(v) }

// Int64 returns a pointer to v.
func Int64(v int64) *int64 { return serverpb.Int64(v) }

// Uint32 returns a pointer to v.
func Uint32(v uint32) *uint32 { return serverpb.Uint32(v) }

// Uint64 returns a pointer to v.
func Uint64(v uint64) *uint64 { return serverpb.Uint64(v) }

// Bool returns a pointer to v.
func Bool(v bool) *bool { return serverpb.Bool(v) }

// String returns a pointer to v.
func String(v string) *string { return serverpb.String(v) }
