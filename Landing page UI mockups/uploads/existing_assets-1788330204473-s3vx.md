# Secure Digital DMS for India's Investigative & Judicial Workflows
### Master Project Specification — Smart India Hackathon 2026

> This document consolidates the research paper, frontend spec, working concept, tech stack, and
> project structure into a single authoritative reference. Where earlier drafts conflicted, the
> more recent, detailed decision is treated as canonical (noted inline).

---

## 1. One-Line Pitch

A role-based, cryptographically secure Document Management System that gives India's police,
forensic labs, lawyers, and judges one unified, tamper-evident place to manage case evidence —
proving chain-of-custody instead of just claiming it.

---

## 2. Problem Statement

India's investigative and judicial process runs on fragmented, disconnected record-keeping:

- **Ecosystem fragmentation** — police stations, the CBI, forensic science laboratories (FSLs),
  and district/high courts each maintain their own proprietary, non-communicating records.
- **Integrity vulnerabilities** — physical case files degrade, get lost, or get tampered with;
  existing digital repositories lack granular access control and verifiable audit trails.
- **Version control failure** — multiple unverified copies of the same document circulate,
  making it impossible for lawyers or judges to confirm which copy is authoritative.

### Existing landscape

| Category | Examples | Gap |
|---|---|---|
| Commercial legal-tech | MyKase, CLAW, corporate compliance tools | Built for civil litigation / contracts, no criminal-justice integration |
| Government frameworks | ICJS, CCTNS, eCourts e-Filing | Connect major data pillars but remain cumbersome, siloed, not built for real-time collaborative workflows |
| **This project** | — | Automated cryptographic evidence hashing, live cross-agency handoff, automated §65B certification — none of the above do all three |

---

## 3. Solution Overview

A single web application where every document is:

1. **Hashed on intake** — SHA-256 fingerprint generated immediately on upload.
2. **Access-controlled by role** — RBAC/ABAC ensures judges, lawyers, police, and forensics staff
   each see only what they're entitled to (e.g., witness identities hidden from unauthorized roles).
3. **Logged immutably** — every read, upload, export, share, or modification is written to an
   append-only, hash-chained audit log. Tampering with a past entry breaks the chain visibly.
4. **Certifiable** — the system auto-generates §65B electronic-record certificates required for
   admissibility of digital evidence under Indian law.

### End-to-end user flow

1. **Login** — email/password; system resolves role (judge, lawyer, police, forensics, admin).
2. **Dashboard** — role-specific: police see open cases/uploads, judges see docket, lawyers see
   attached cases, forensics see pending analysis requests.
3. **Open a case** — see all documents, involved parties, status, and a chronological timeline.
4. **Upload a document** — e.g. a forensic report; system generates and stores a SHA-256 hash.
   Any later change to the file invalidates the hash, proving tampering.
5. **Verify a document** — "Verify Integrity" button re-computes the hash live and shows a
   green (unaltered) or red (tampered) result.
6. **Audit logging** — every action writes a permanent, chain-linked log entry; entries cannot be
   silently edited or deleted without breaking the chain.
7. **Redact sensitive info** — draw-to-redact in-browser creates a *new*, separately hashed copy;
   the original stays locked away untouched.
8. **Cross-role sharing** — documents move from police → prosecutor → court digitally, with every
   handoff automatically logged instead of relying on physical handoff or insecure email.

---

## 4. Legal & Regulatory Alignment

| Law / Standard | Relevance |
|---|---|
| **Indian Evidence Act, 1872** (and successor **Bharatiya Sakshya Adhiniyam, 2023**) | Governs admissibility of digital evidence; requires verifiable hash values and unbroken chain of custody |
| **Information Technology Act, 2000 — §65A / §65B** | Authenticity and certification standards for electronic records; system auto-generates §65B certificates |
| **Digital Personal Data Protection Act (DPDP)** | Balances RTI-driven public disclosure rights against mandatory privacy protection for sensitive investigation data (e.g. juvenile records, witness identities) |

---

## 5. Core Security Architecture

- **Cryptographic chain of custody** — SHA-256 hash generated on every document at ingest;
  re-verifiable on demand at any later point.
- **RBAC / ABAC** — dynamic permission matrix; restricted materials (witness identities, juvenile
  records) visible only to authorized personas.
- **Immutable audit trail** — append-only transaction log; every read/export/modify/view event
  recorded with timestamp + user ID; each entry hash-linked to the previous one (chain structure).
- **Automated §65B certification** — system compiles electronic-record metadata into the
  certificate required for admissibility.
- **Encryption** — AES-256 at rest for sensitive document categories; TLS in transit.

---

## 6. Technology Stack (Authoritative)

> **Note on stack evolution:** An earlier draft of this project considered a Python
> (Flask/FastAPI) or Node.js (Express) backend with plain PostgreSQL. The team has since
> finalized on the stack below — Go/Gin with sqlc and Row-Level Security — as it gives stronger
> compile-time guarantees around the audit-log immutability logic, which is the project's core
> differentiator. The stack below is the one actually implemented.

### 6.1 Backend

| Layer | Choice | Why |
|---|---|---|
| Language | Go 1.22+ | Fast, concurrent, compiles to a single binary — clean demo deployment |
| Web framework | Gin (`gin-gonic/gin`) | Mature, minimal, easy middleware chaining (auth → RBAC → audit) |
| DB access | sqlc (`sqlc-dev/sqlc`) | Type-safe generated Go from raw SQL — full control for audit-log immutability |
| Auth | JWT (`golang-jwt/jwt/v5`) | Short-lived access + refresh tokens |
| Password hashing | bcrypt (`golang.org/x/crypto/bcrypt`) | Industry standard |
| Validation | `go-playground/validator` | Request payload validation |
| Config | `godotenv` / `viper` | Environment-based config |
| Migrations | `golang-migrate/migrate` | Versioned SQL migrations |
| API docs | `swaggo/swag` | Auto-generated OpenAPI/Swagger UI from Go comments |

### 6.2 Database

| Component | Choice | Why |
|---|---|---|
| Primary DB | PostgreSQL 15+ | Relational integrity for case↔document↔user↔audit relationships |
| Flexible metadata | JSONB columns | Per-document-type fields (FIR details, forensic report fields, etc.) |
| Access enforcement | Row-Level Security (RLS) | DB-layer RBAC — a compromised app layer still can't bypass it |
| Audit table | Append-only, hash-chained | No UPDATE/DELETE grants at DB role level — immutability enforced structurally |

### 6.3 File / Object Storage

| Component | Choice | Why |
|---|---|---|
| Object store | MinIO (S3-compatible, self-hosted) | Stores actual documents; Postgres stores only hash + object reference |
| Structure | Per-case / per-agency bucket or prefix | Isolation between agencies |

### 6.4 Security & Integrity

| Concern | Choice | Why |
|---|---|---|
| Document integrity | SHA-256 on upload, re-verified on retrieval | Maps directly to IT Act §65B / Evidence Act admissibility |
| Digital signatures (stretch) | RSA/ECDSA (`crypto/rsa`, `crypto/ecdsa`) | Non-repudiation on finalized documents |
| Encryption at rest | AES-256 (`crypto/aes`) | For sensitive document categories before writing to MinIO |
| Transport security | TLS everywhere (mkcert for local self-signed certs) | Baseline hygiene |
| RBAC | Roles table + permissions table + middleware + RLS | Judge / Lawyer / Police / Forensics / Admin scoping |

### 6.5 Frontend

| Layer | Choice | Why |
|---|---|---|
| Framework | React 18 + TypeScript | Type safety, large ecosystem |
| Build tool | Vite | Fast dev server, quick iteration |
| Styling | Tailwind CSS + shadcn/ui | Professional look, minimal design overhead |
| Server state | TanStack Query (`@tanstack/react-query`) | Clean caching for case/document/audit data |
| Forms | `react-hook-form` + `zod` | Validation |
| Routing | `react-router-dom` | Standard SPA routing |
| Charts | `recharts` | Audit dashboard, activity timelines |
| Icons | `lucide-react` | Outline-style icon set, matches design system |

### 6.6 DevOps / Infra

| Component | Choice | Why |
|---|---|---|
| Containerization | Docker + Docker Compose | One-command spin-up (API + Postgres + MinIO + frontend) — demo reliability |
| CI | GitHub Actions (lint + build) | Signals engineering maturity |
| Env separation | `.env.example` committed, `.env` gitignored | Security hygiene |

### 6.7 Testing

| Type | Tool | Focus |
|---|---|---|
| Unit/integration | Go `testing` + `testify` | Hash verification, RBAC middleware, audit-log immutability |

### 6.8 Documentation

| Artifact | Tool |
|---|---|
| API reference | Swagger/OpenAPI via swaggo |
| Architecture diagram | Draw.io / Excalidraw, checked into `docs/ARCHITECTURE.md` |

### 6.9 Suggested Team Split (3 people)

- **Person A** — Go backend core: auth, RBAC middleware, case/document APIs
- **Person B** — Audit logging + hashing/integrity system + Postgres schema/RLS (core differentiator)
- **Person C** — React frontend + Swagger docs + Docker Compose setup

---

## 7. Frontend Design System

**Tone:** Government / institutional, minimal, professional, trustworthy. No playful color, no
marketing gloss. Every visual decision should read as "this is where evidence lives."

### 7.1 Color Palette

| Element | Color | Usage |
|---|---|---|
| Primary / Navbar | `#132A49` | Top nav, primary buttons, active states |
| Page Background | `#E6EAED` | App shell background |
| Cards | `#FFFFFF` | All card/panel surfaces |
| Secondary Surface | `#F5F6F8` | Table row alternation, input backgrounds, nested panels |
| Primary Text | `#172536` | Headings, body copy |
| Secondary Text | `#68717C` | Labels, timestamps, metadata, helper text |
| Borders | `#E2E6EA` | 1px card/table/input borders |
| Icons | `#A7AFB7` | Default icon color (outline style) |
| Links | `#426D9B` | Inline links, secondary actions |

**Status colors** (used sparingly — badges, icons, thin left-borders, never full-card backgrounds):

| State | Color | Usage |
|---|---|---|
| Success / Verified | `#2E7D4F` (muted green) | Hash verified, chain intact |
| Warning | `#B8860B` (muted amber) | Pending review, expiring retention |
| Danger / Tampered | `#B23B3B` (muted red) | Hash mismatch, chain broken, access denied |
| Info | `#426D9B` | Neutral notices |

### 7.2 Typography

- **Font:** Inter (all weights)
- **H1:** 36–40px / 700 — page titles only (one per page)
- **H2:** 20–22px / 600 — section headers, card titles
- **Body:** 14–16px / 400 — default text
- **Small:** 12–13px / 400 — timestamps, metadata, table secondary text, badges

### 7.3 UI Style Rules

- **Cards:** white fill, 1px `#E2E6EA` border, 4–6px radius, no shadow or near-invisible shadow
- **Buttons:** navy primary fill / white text, rectangular-leaning, 4–6px radius; secondary =
  white with navy border/text; destructive = muted danger red, sparingly
- **Icons:** outline style only, default `#A7AFB7`, darken on hover/active (`lucide-react`)
- **Spacing:** dense-but-not-cramped, 8px base unit
- **Layout:** grid-based, mostly 2-column where content allows
- **Data tables:** zebra-striped (`#F5F6F8`), thin dividers, sortable headers with sort icons
- **Empty/loading states:** skeleton loaders over spinners; empty states = muted icon + one line
  of secondary text, no illustrations

---

## 8. Global Layout

Persistent left sidebar (role-aware nav) + top navbar + main content area.

- **Top navbar** (`#132A49`, white text/icons): system name/logo (left), global search (center),
  notifications + avatar/role badge + logout (right)
- **Left sidebar** (white/`#F5F6F8`, right border): nav items change per role. Active item gets
  navy left-border accent + navy text.
- **Breadcrumbs:** shown on any page nested more than one level
- **Main content area:** `#E6EAED` background, cards/panels floated on top

---

## 9. Pages & Components

### 9.1 Login — `/login`
Centered card, no sidebar/navbar. System name/logo, tagline, email + password, primary "Login"
button, institutional footer note ("Authorized personnel only — all access is logged").
**Component:** `LoginForm`

### 9.2 Role-Aware Dashboard — `/`
Grid of summary cards + recent-activity feed, content varies by role:
- Police: open cases, recent uploads, pending redactions
- Judge: docket, upcoming hearings, flagged integrity issues
- Lawyer: attached cases, shared documents, disclosure deadlines
- Forensics: pending analysis requests, recently submitted reports

**Components:** `SummaryStatCard`, `RecentActivityFeed`, `QuickActionsPanel`

### 9.3 Case List — `/cases`
Table: Case Number | Title | Status (badge) | Created By | Last Updated | Documents count.
Filters (status, date range, agency), search, "New Case" button (police/admin only).
**Components:** `DataTable`, `StatusBadge`, `FilterBar`, `SearchInput`, `Pagination`

### 9.4 Case Detail — `/cases/:id`
Two-column: (left) case metadata + document list with hash badges; (right) case timeline,
involved parties list.
**Components:** `CaseMetaCard`, `DocumentGrid`/`DocumentListItem`, `CaseTimeline`,
`InvolvedPartiesList`, `UploadDocumentButton`

### 9.5 Document Upload — modal/drawer or `/cases/:id/documents/upload`
Drag-and-drop dropzone, file preview, metadata fields (type, description), submit → progress bar
→ hash generation animation → success state showing SHA-256 hash (monospace, copyable).
**Components:** `FileDropzone`, `UploadProgressBar`, `HashRevealCard`, `DocumentTypeSelect`

### 9.6 Document Viewer — `/documents/:id`
Left: inline preview (PDF.js-style / `<img>` / fallback icon). Right (sticky):
- **Integrity panel** — hash (monospace, truncated), "Verify Integrity" button, result badge
- **Compliance badge** — "IT Act §65B Compliant" with certificate-detail tooltip
- **Custody timeline** — Uploaded → Viewed → Redacted → Shared → Archived, hash-linked nodes
- **Actions** — Download, Redact, Share (role-permitting)

**Components:** `DocumentPreviewPane`, `IntegrityVerifyPanel`, `ComplianceBadge`,
`CustodyTimeline`, `DocumentActionBar`

### 9.7 Redaction Tool — `/documents/:id/redact`
Full-width canvas, draw-to-redact black boxes, sidebar list of applied redactions (removable,
optional "Reason" field), Save → hash-reveal treatment framed as "Redacted copy created —
original preserved separately."
**Components:** `RedactionCanvas`, `RedactionListPanel`, `HashRevealCard` (reused)

### 9.8 Audit Log — `/audit` (flagship differentiator screen)
Table: Timestamp | User | Role | Action | Resource (expandable rows show hash + prev-hash).
Filters (user, case, action type, date range).
- **"Verify Chain" button** — live re-walk of the entire hash chain; progress indicator
  ("Verifying entry 1,204 / 1,204...") → success banner ("✓ Chain intact — 1,204 entries
  verified") or danger banner pinpointing a broken link
- **Visual chain graph** — secondary tab rendering entries as connected nodes

**Components:** `AuditLogTable`, `ChainVerifyButton`, `ChainVerifyResultBanner`,
`AuditChainGraph`, `AuditEntryDetail`

### 9.9 Role Comparison / Access Demo — `/cases/:id/access-preview` (optional, high-impact)
Side-by-side or toggle view of the same case/document as seen by two different roles, with
restricted fields greyed out/blurred and labeled "Restricted — Role: Police only."
**Components:** `RoleToggle`, `RestrictedFieldOverlay`

### 9.10 User/Case Management (Admin) — `/admin/users`, `/admin/roles`
Table-based CRUD for users and role assignment. Functional over polished — lowest design priority.

---

## 10. Role-Aware Navigation

| Nav Item | Judge | Lawyer | Police | Forensics | Admin |
|---|---|---|---|---|---|
| Dashboard | ✓ | ✓ | ✓ | ✓ | ✓ |
| Cases | ✓ (docket) | ✓ (assigned) | ✓ (all/own) | ✓ (linked) | ✓ (all) |
| Upload Document | — | — | ✓ | ✓ | ✓ |
| Audit Log | ✓ | — | limited | — | ✓ |
| User Management | — | — | — | — | ✓ |

---

## 11. Signature Interactions ("Groundbreaking" Demo Moments)

Extra design/engineering polish here is what turns backend features into a memorable demo.

| # | Interaction | Behavior |
|---|---|---|
| 6.1 | **Hash Generation Reveal** (upload) | Monospace hash string types/scrambles then resolves to final SHA-256 value → green check settles in. ~1.5–2s. |
| 6.2 | **Live Integrity Verification** | Brief inline spinner ("Recomputing hash...") before resolving to badge — never instant, the visible computation is the point |
| 6.3 | **Chain Verification Sweep** | On Audit Log, "Verify Chain" visually sweeps the chain graph entry-by-entry before landing on intact/broken banner — **highest-value animation in the app**, budget the most design attention here |
| 6.4 | **Redaction Draw** | Real-time black-box drawing with immediate visual feedback, followed by the hash-reveal treatment for the new redacted copy |
| 6.5 | **Compliance Badge Hover** | Hovering "IT Act §65B Compliant" reveals a popover with auto-generated certificate metadata (hash, timestamp, signing authority) |

---

## 12. Shared / Utility Components

`AppShell` · `DataTable` · `StatusBadge` · `SearchInput` · `FilterBar` · `Modal`/`Drawer` ·
`Toast` · `Tooltip`/`Popover` · `Avatar` · `EmptyState` · `SkeletonLoader` · `Pagination` ·
`Timeline` (base component powering `CaseTimeline` and `CustodyTimeline`) · `IconButton`

---

## 13. Accessibility & Trust Cues

- Every page footer: "All access to this system is logged" (reinforces the audit story ambiently)
- Consistent, sober iconography — no emoji, no illustration-style graphics
- High color contrast on all text — palette checked against WCAG AA minimum
- Clear, unambiguous role labeling always visible (avatar badge showing current role)
- No dark patterns — Redact/Delete always require explicit confirmation (`ConfirmDialog`)

---

## 14. Build Priority Order (if time-constrained)

1. Login + Dashboard (role-aware)
2. Case List + Case Detail
3. Document Viewer + Integrity Panel (core differentiator)
4. Audit Log + Chain Verify (flagship differentiator — invest most polish here)
5. Document Upload (with hash reveal)
6. Redaction Tool
7. Role Comparison / Access Demo (nice-to-have, very high demo impact if time allows)
8. Admin pages (lowest priority — functional only)

---

## 15. Repository Structure

Monorepo: Go backend + React/TypeScript frontend.

```
secure-dms/
├── .env.example
├── .gitignore
├── LICENSE
├── README.md
├── TECH_STACK.md
├── docker-compose.yml
├── .github/
│   └── workflows/
│       └── ci.yml
├── backend/
│   ├── .env.example
│   ├── Dockerfile
│   ├── Makefile
│   ├── go.mod
│   ├── go.sum
│   ├── cmd/
│   │   └── server/
│   │       └── main.go
│   ├── docs/
│   ├── internal/
│   │   ├── audit/              # hash-chain audit log — core differentiator
│   │   │   ├── chain.go
│   │   │   └── logger.go
│   │   ├── auth/
│   │   │   ├── jwt.go
│   │   │   └── password.go
│   │   ├── config/
│   │   │   └── config.go
│   │   ├── handlers/
│   │   │   ├── audit/
│   │   │   │   ├── list.go
│   │   │   │   └── verify_chain.go
│   │   │   ├── case/
│   │   │   │   ├── create.go
│   │   │   │   ├── get.go
│   │   │   │   ├── list.go
│   │   │   │   └── update.go
│   │   │   ├── document/
│   │   │   │   ├── download.go
│   │   │   │   ├── redact.go
│   │   │   │   ├── upload.go
│   │   │   │   └── verify.go
│   │   │   └── user/
│   │   │       ├── login.go
│   │   │       ├── profile.go
│   │   │       └── register.go
│   │   ├── middleware/
│   │   │   ├── audit_middleware.go
│   │   │   ├── auth_middleware.go
│   │   │   ├── cors_middleware.go
│   │   │   └── rbac_middleware.go
│   │   ├── models/
│   │   │   ├── audit_log.go
│   │   │   ├── case.go
│   │   │   ├── document.go
│   │   │   ├── role.go
│   │   │   └── user.go
│   │   ├── repository/
│   │   │   ├── audit_repo.go
│   │   │   ├── case_repo.go
│   │   │   ├── document_repo.go
│   │   │   └── user_repo.go
│   │   ├── service/
│   │   │   ├── audit_service.go
│   │   │   ├── case_service.go
│   │   │   └── document_service.go
│   │   ├── storage/
│   │   │   ├── local_storage.go
│   │   │   └── minio_client.go
│   │   └── utils/
│   │       ├── errors.go
│   │       └── validator.go
│   ├── migrations/
│   │   ├── 000001_init_schema.down.sql
│   │   └── 000001_init_schema.up.sql
│   ├── pkg/
│   │   ├── crypto/
│   │   │   ├── aes.go
│   │   │   └── rsa_sign.go
│   │   ├── hash/
│   │   │   └── sha256.go
│   │   └── response/
│   │       └── response.go
│   └── tests/
│       ├── audit_test.go
│       ├── auth_test.go
│       ├── hash_test.go
│       └── rbac_test.go
├── docs/
│   ├── API_ENDPOINTS.md
│   ├── ARCHITECTURE.md
│   └── DATABASE_SCHEMA.md
├── frontend/
│   ├── .env.example
│   ├── Dockerfile
│   ├── index.html
│   ├── package.json
│   ├── postcss.config.js
│   ├── tailwind.config.ts
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── public/
│   └── src/
│       ├── App.tsx
│       ├── index.css
│       ├── main.tsx
│       ├── components/
│       │   ├── layout/
│       │   │   ├── Navbar.tsx
│       │   │   ├── ProtectedRoute.tsx
│       │   │   └── Sidebar.tsx
│       │   └── ui/
│       ├── hooks/
│       │   ├── useAuditLog.ts
│       │   ├── useAuth.ts
│       │   ├── useCases.ts
│       │   └── useDocuments.ts
│       ├── lib/
│       │   ├── api.ts
│       │   ├── queryClient.ts
│       │   └── utils.ts
│       ├── pages/
│       │   ├── audit/
│       │   │   └── AuditLogView.tsx
│       │   ├── auth/
│       │   │   ├── Login.tsx
│       │   │   └── Register.tsx
│       │   ├── cases/
│       │   │   ├── CaseCreate.tsx
│       │   │   ├── CaseDetail.tsx
│       │   │   └── CaseList.tsx
│       │   ├── dashboard/
│       │   │   └── Dashboard.tsx
│       │   └── documents/
│       │       ├── DocumentUpload.tsx
│       │       ├── DocumentViewer.tsx
│       │       └── RedactionTool.tsx
│       ├── routes/
│       │   └── index.tsx
│       ├── store/
│       │   └── authStore.ts
│       └── types/
│           ├── audit.ts
│           ├── case.ts
│           ├── document.ts
│           └── user.ts
└── scripts/
    ├── seed_db.sh
    └── setup.sh
```

### 15.1 Layer Notes

- **`backend/internal/`** — all business logic, organized by concern (not by feature): standard
  Go layout convention.
  - `handlers/` — HTTP request handlers, grouped by resource
  - `middleware/` — auth (JWT), RBAC (role checks), audit (auto-logs every request), CORS
  - `models/` — struct definitions matching DB tables
  - `repository/` — direct DB access via sqlc-generated queries
  - `service/` — business logic between handlers and repositories
  - `audit/` — the hash-chain audit log implementation — **core differentiator**
  - `storage/` — MinIO client + abstraction (swappable for local disk in early dev)
- **`backend/pkg/`** — reusable, standalone packages with no dependency on `internal/` (hashing,
  crypto, response formatting) — kept separate so they could be imported by other services later
  (e.g. a mobile backend, Phase 2).
- **`backend/migrations/`** — versioned SQL applied via `golang-migrate`; `000001_init_schema`
  sets up roles, users, cases, documents, and the append-only `audit_log` table.
- **`frontend/src/pages/`** — one folder per feature area, matching the demo narrative:
  auth → dashboard → cases → documents → audit.
- **`frontend/src/hooks/`** — TanStack Query hooks wrapping `lib/api.ts`, keeping data-fetching
  logic out of components.
- **`docker-compose.yml`** — spins up Postgres + MinIO + backend + frontend together; this is
  what runs live during the demo (`docker-compose up --build`).
- **`.github/workflows/ci.yml`** — basic build/vet/lint check on push, a real signal of
  engineering discipline for judges reviewing the repo.

---

## 16. Summary

This project turns three abstract legal-tech promises — **integrity, access control, and
auditability** — into things a judge can *see* happen live: a hash resolving on screen, a
chain-verification sweep landing on "intact," a redaction instantly producing a separately
hashed copy while the original stays sealed. The backend (Go + Postgres RLS + MinIO) is built to
make those guarantees structurally true, not just cosmetically displayed, and the frontend is
designed to make that truth visible and legible to non-technical stakeholders — investigators,
lawyers, and judges — in a demo setting.
