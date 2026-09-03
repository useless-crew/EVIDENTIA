// Local development environment config — used by `ng serve` (the dev
// server's default configuration, see angular.json) and
// `ng build --configuration development`, via angular.json's
// fileReplacements. Points at the backend started by the repository's
// docker-compose.yml (BACKEND_PORT default 8080 — see ../../docker-compose.yml
// and .env.example) or `go run ./cmd/server` directly on the same port.
//
// This is the Angular-idiomatic equivalent of a Vite `.env`/`VITE_*`
// variable: Angular's esbuild-based CLI builder does not read `.env`
// files, so environment.*.ts + angular.json fileReplacements is the
// project's real, functioning mechanism for an environment-specific API
// base URL — see frontend/README.md's "Configuration" section.
export const environment = {
  production: false,
  apiBaseUrl: 'http://localhost:8080/api/v1',
};
