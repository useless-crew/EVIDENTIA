package app

import (
	"evidentia/backend/internal/cache"
	"evidentia/backend/internal/database"
)

// Compile-time assertions that the real infrastructure clients satisfy the
// narrow interfaces App depends on. If either concrete type's method set
// ever drifts from these interfaces, this file fails to compile — a faster
// signal than discovering it via a runtime type assertion failure.
var (
	_ DBConn    = (*database.Database)(nil)
	_ CacheConn = (*cache.Cache)(nil)
)
