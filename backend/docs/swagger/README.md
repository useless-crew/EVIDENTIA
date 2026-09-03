# Swagger Output Directory

Generated OpenAPI/Swagger artifacts (`docs.go`, `swagger.json`,
`swagger.yaml`) are written here by `make swagger` (requires the `swag`
CLI: https://github.com/swaggo/swag#getting-started). They are gitignored
— regenerate after changing any handler's Swagger annotations — since
nothing in the running application imports this package (unlike
`backend/db/generated`, an empty directory here is harmless to a fresh
clone).

Documents every implemented route — authentication
(`POST /api/v1/auth/{login,refresh,logout}`), cases, case documents,
document verify/certificate, and admin user management
(`/api/v1/admin/*`, `/api/v1/users/me`) — see the `@title`/`@version`
block above `func main` in `backend/cmd/server/main.go` and the
`@Summary`/`@Success`/`@Failure` annotations on each handler in
`backend/internal/handlers/*/`.

No live `/swagger/index.html` UI is served yet (that needs
`swaggo/gin-swagger` wired into the router, not part of this system's
scope) — view the generated `swagger.json`/`swagger.yaml` directly, or
paste `swagger.json` into https://editor.swagger.io.
