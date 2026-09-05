// Package app assembles Evidentia's application container: configuration,
// logger, and infrastructure clients (PostgreSQL, Redis, MinIO), each
// initialized exactly once and passed by dependency injection to whatever
// depends on it — never as package-level globals. Later systems add fields
// here (or take *App as a constructor argument) rather than reaching for
// their own global connections.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"

	"evidentia/backend/internal/audit"
	"evidentia/backend/internal/auth"
	"evidentia/backend/internal/authz"
	"evidentia/backend/internal/cache"
	"evidentia/backend/internal/config"
	"evidentia/backend/internal/database"
	"evidentia/backend/internal/events"
	"evidentia/backend/internal/jobs"
	"evidentia/backend/internal/logger"
	"evidentia/backend/internal/service"
	"evidentia/backend/internal/sse"
	"evidentia/backend/internal/storage"
)

// App holds every foundational dependency the HTTP server and, later,
// handlers/services/repositories need. It has exactly one owner (main),
// which is responsible for calling Close during shutdown.
type App struct {
	Config      *config.Config
	Logger      *slog.Logger
	DB          DBConn
	Cache       CacheConn
	Storage     storage.Storage
	JWTManager  *auth.JWTManager
	AuthService *service.AuthService

	// AuthzService is System 4's centralized RBAC+ABAC engine (see
	// internal/authz) — separate from AuthService (System 3, "who is this
	// user") because it answers a different question ("what may this user
	// do"). Later systems' handlers/routes depend on this field to guard
	// their case/document/audit/admin routes; see
	// internal/middleware.RequirePermission/RequireCaseAccess/
	// RequireDocumentAccess.
	AuthzService *authz.Service

	// CaseService is System 5's case-management business logic (see
	// internal/service.CaseService) — owns validation, transactional
	// persistence, status-transition rules, and audit integration for
	// POST/GET/PUT /cases. Depends on AuthzService for its own
	// service-layer authorization re-check (see that type's doc comment).
	CaseService *service.CaseService

	// DocumentService is System 6's document-ingestion/retrieval business
	// logic (see internal/service.DocumentService) — owns multipart
	// validation, streaming SHA-256 computation, object storage via
	// Storage, PostgreSQL metadata persistence, and audit integration for
	// POST /cases/:id/documents and GET /documents/:id/download. System 7
	// extends it with VerifyDocument (POST /documents/:id/verify).
	DocumentService *service.DocumentService

	// CertificateService is System 7's compliance-certificate business
	// logic (see internal/service.CertificateService) — re-verifies a
	// document's integrity, signs and persists a certificate bound to its
	// exact canonical hash, and audit integration for
	// GET /documents/:id/certificate.
	CertificateService *service.CertificateService

	// UserService is System 8's admin user-management business logic (see
	// internal/service.UserService) — user CRUD, role assignment, account
	// status, and admin-initiated password reset for
	// /api/v1/admin/users*/roles and /api/v1/users/me.
	UserService *service.UserService

	// ShareService owns secure document sharing & access delegation (see
	// internal/service.ShareService) — share creation/listing/revocation,
	// the "Shared With Me" listing, and recipient search for
	// POST/GET /documents/:id/share(s), the revoke route,
	// GET /shared/documents, and GET /users/search. Depends on
	// AuthzService for authorization exactly like every other document
	// route (authz.ActionDocumentShare) and shares its
	// delegated-access-check logic with CanAccessDocument itself (see
	// internal/authz/share_policy.go) — no separate authorization engine.
	ShareService *service.ShareService

	// AuditService owns audit-trail retrieval and cryptographic chain
	// verification (see internal/service.AuditService) for GET /audit and
	// the System 11 audit-verification routes. Audit event RECORDING is
	// not this field's job — every other service already records through
	// the shared audit.Recorder (see the ChainWriter constructed below).
	AuditService *service.AuditService

	// JobClient is System 11's Asynq task-enqueueing client (see
	// internal/jobs.Client) — the only way AuditService dispatches a
	// background verification. The corresponding WORKER (asynq.Server +
	// ServeMux processing tasks) is deliberately NOT a field here: it is
	// constructed and run by cmd/server/main.go alongside the HTTP
	// server's own listen/shutdown lifecycle, embedded in this same
	// process/binary rather than a separate deployment unit (see that
	// file's own doc comment for why) — app.go itself only owns the
	// Redis-backed CLIENT connection, exactly like it owns Cache/DB/
	// Storage above.
	JobClient *jobs.Client

	// SSEManager is System 13's ONE Server-Sent Events fan-out (see
	// internal/sse.Manager) — subscribes to Redis Pub/Sub
	// (internal/events.Channel) once, at process start, and delivers each
	// received event to whichever locally-connected, already-authorized
	// clients are registered for its exact resource scope.
	// internal/handlers/audit and internal/handlers/case's Events routes
	// both register against this SAME instance — never a second manager.
	// Every service that PUBLISHES a notification (AuditService,
	// DocumentService, CertificateService, ShareService) depends on
	// internal/events.Publisher instead, which this field's Manager
	// receives from independently, through Redis — there is no direct Go
	// dependency between a business service and this field at all (see
	// docs/REALTIME_EVENTS.md's own architecture diagram).
	SSEManager *sse.Manager
}

// New loads configuration and connects every infrastructure dependency in
// turn, failing fast (and unwinding anything already connected) if any step
// fails — the application must never start in a partially initialized
// state. ctx bounds how long each connection attempt may take.
func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	log, err := logger.New(cfg.Logging.Level, cfg.Logging.Format)
	if err != nil {
		return nil, fmt.Errorf("app: build logger: %w", err)
	}

	db, err := database.New(ctx, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("app: connect database: %w", err)
	}

	redisCache, err := cache.New(ctx, cfg.Redis)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("app: connect redis: %w", err)
	}

	objectStorage, err := storage.NewMinIO(ctx, cfg.MinIO)
	if err != nil {
		db.Close()
		_ = redisCache.Close()
		return nil, fmt.Errorf("app: connect storage: %w", err)
	}

	// ChainWriter is the authoritative, cryptographically hash-chained
	// audit.Recorder (internal/audit/writer.go) — replacing
	// audit.SlogRecorder (System 3's original operational-log-only
	// placeholder) here is the ENTIRE integration point that makes every
	// existing recorder.Record call across Systems 3-9 (auth/case/
	// document/certificate/redact/share services) start durably,
	// tamper-evidently persisting to audit_log instead of only the
	// structured operational log — none of those call sites change.
	recorder := audit.NewChainWriter(db.Pool(), log)

	// eventPublisher is System 13's ONE real-time-notification publisher
	// (see internal/events) — reuses the SAME shared go-redis client
	// internal/cache.Cache already validated at startup (redisCache.
	// Client()), never a second Redis connection pool. Every service
	// constructed below that publishes a notification (AuditService,
	// DocumentService, CertificateService, ShareService) takes this ONE
	// instance — never its own ad hoc redis.Publish call.
	eventPublisher := events.NewRedisPublisher(redisCache.Client(), log)

	// sseManager is System 13's ONE SSE fan-out (see internal/sse) —
	// subscribes to the SAME Redis channel eventPublisher publishes to.
	// Constructed here (so every handler needing it can depend on this one
	// instance) but not yet STARTED: cmd/server/main.go launches
	// SSEManager.Start in its own goroutine, bound to the same top-level
	// shutdown context as the HTTP server and the Asynq worker.
	sseManager := sse.NewManager(redisCache.Client(), log)

	jwtManager := auth.NewJWTManager(cfg.JWT.SigningKey, cfg.JWT.Issuer, cfg.JWT.Audience, cfg.JWT.AccessTTL)
	authService := service.NewAuthService(db.Pool(), jwtManager, cfg.JWT.BcryptCost, cfg.JWT.RefreshTTL, recorder)
	authzService := authz.NewService(db.Pool(), recorder)
	caseService := service.NewCaseService(db.Pool(), authzService, recorder)
	documentService := service.NewDocumentService(db.Pool(), authzService, recorder, objectStorage, cfg.MinIO.Bucket, cfg.Documents.MaxUploadSize, eventPublisher, log)
	certificateService, err := service.NewCertificateService(db.Pool(), authzService, recorder, objectStorage, cfg.Certificate.SigningKeyPEM, eventPublisher, log)
	if err != nil {
		db.Close()
		_ = redisCache.Close()
		return nil, fmt.Errorf("app: build certificate service: %w", err)
	}
	userService := service.NewUserService(db.Pool(), authzService, recorder, cfg.JWT.BcryptCost)
	shareService := service.NewShareService(db.Pool(), authzService, recorder, eventPublisher)

	// asynq.RedisClientOpt mirrors cfg.Redis exactly — Asynq manages its
	// own connection pool independently of internal/cache.Cache's
	// go-redis client (they are two separate connections to the same
	// Redis instance, not a shared pool: Asynq's client/server API only
	// accepts its own RedisConnOpt types, not an existing *redis.Client).
	redisOpt := asynq.RedisClientOpt{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB}
	jobClient := jobs.NewClient(redisOpt)
	auditService := service.NewAuditService(db.Pool(), authzService, recorder, jobClient, eventPublisher, log)

	return &App{
		Config:             cfg,
		Logger:             log,
		DB:                 db,
		Cache:              redisCache,
		Storage:            objectStorage,
		JWTManager:         jwtManager,
		AuthService:        authService,
		AuthzService:       authzService,
		CaseService:        caseService,
		DocumentService:    documentService,
		CertificateService: certificateService,
		UserService:        userService,
		ShareService:       shareService,
		AuditService:       auditService,
		JobClient:          jobClient,
		SSEManager:         sseManager,
	}, nil
}

// Close releases every infrastructure dependency. Called once during
// graceful shutdown, after the HTTP server has stopped accepting new
// requests (see cmd/server/main.go for the full shutdown sequence).
func (a *App) Close() {
	if err := a.JobClient.Close(); err != nil {
		a.Logger.Error("shutdown: closing asynq client", slog.String("error", err.Error()))
	} else {
		a.Logger.Info("shutdown: asynq client closed")
	}

	if err := a.Cache.Close(); err != nil {
		a.Logger.Error("shutdown: closing redis", slog.String("error", err.Error()))
	} else {
		a.Logger.Info("shutdown: redis closed")
	}

	// MinIO's client is a thin HTTP wrapper with no persistent connection
	// pool of its own to release explicitly; nothing to do here beyond
	// acknowledging the shutdown step exists in the sequence.

	a.DB.Close()
	a.Logger.Info("shutdown: database pool closed")
}
