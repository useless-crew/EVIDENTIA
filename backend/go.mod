module evidentia/backend

go 1.22

// TODO: Add dependency requirements as implementation begins, module-by-module.
//
// Expected dependencies (per TECH_STACK.md / ARCHITECTURE.md) — none are
// wired in yet since no code uses them:
//
//   github.com/gin-gonic/gin              — REST framework
//   github.com/golang-jwt/jwt/v5          — JWT auth
//   golang.org/x/crypto                   — bcrypt, AES/RSA/ECDSA primitives
//   github.com/go-playground/validator/v10 — request validation
//   github.com/jackc/pgx/v5               — PostgreSQL driver
//   github.com/golang-migrate/migrate/v4  — schema migrations
//   github.com/minio/minio-go/v7          — MinIO S3-compatible client
//   github.com/redis/go-redis/v9          — Redis client
//   github.com/hibiken/asynq              — async task queue
//   github.com/joho/godotenv              — .env loading
//   github.com/spf13/viper                — configuration
//   github.com/swaggo/swag                — OpenAPI/Swagger generation
//   github.com/swaggo/gin-swagger         — Swagger UI for Gin
//   github.com/stretchr/testify           — testing assertions
//
// Do NOT add libraries outside the approved stack in TECH_STACK.md.
