package pb

// Pointer helpers for optional protobuf fields (used by generated Delta structs).

// Float32 returns a pointer to v.
func Float32(v float32) *float32 { return &v }

// Float64 returns a pointer to v.
func Float64(v float64) *float64 { return &v }

// Int32 returns a pointer to v.
func Int32(v int32) *int32 { return &v }

// Int64 returns a pointer to v.
func Int64(v int64) *int64 { return &v }

// Uint32 returns a pointer to v.
func Uint32(v uint32) *uint32 { return &v }

// Uint64 returns a pointer to v.
func Uint64(v uint64) *uint64 { return &v }

// Bool returns a pointer to v.
func Bool(v bool) *bool { return &v }

// String returns a pointer to v.
func String(v string) *string { return &v }
