// Production environment config (Angular CLI's standard environments +
// fileReplacements convention — see angular.json's build.configurations).
// `ng build` (production configuration, the build target's default) uses
// this file as-is; `ng serve`/`ng build --configuration development` swap
// in environment.development.ts instead — see that file's own comment for
// why local dev needs a different apiBaseUrl.
//
// apiBaseUrl is relative/same-origin on purpose: a production deployment
// is expected to serve the frontend and reverse-proxy /api/v1 to the
// backend from the same origin (e.g. via nginx/an ingress), so no
// origin/port needs to be hardcoded here. If a deployment instead serves
// the backend from a different origin, override this file's value at
// build time for that deployment — never hardcode a specific production
// domain into source control.
export const environment = {
  production: true,
  apiBaseUrl: '/api/v1',
  // Never true here — see environment.development.ts's comment. A
  // production build must render no demo/role-selection login UI.
  demoMode: false,
};
