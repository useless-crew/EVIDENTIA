import { Routes } from '@angular/router';
import { authGuard } from './core/guards/auth.guard';
import { adminGuard } from './core/guards/admin.guard';

// Route tree replacing the previous single `dms.screen()` signal switch
// (app.component.html used to *ngIf between every screen component with
// no real URL underneath any of them — see the git history for that
// version). Real, guarded, URL-addressable routes are what makes
// "protected routes work", "no broken routes", "refresh works", and
// "deep links work" (master prompt §12/§42) actually true rather than
// aspirational: every one of these paths is a real browser location.
//
// Every screen COMPONENT below is reused as-is (see app.component's old
// template for the mapping) — only how they're reached changed.
export const routes: Routes = [
  {
    path: '',
    redirectTo: 'landing',
    pathMatch: 'full',
  },
  {
    path: 'landing',
    loadComponent: () => import('./screens/landing-page/landing-page.component').then((m) => m.LandingPageComponent),
  },
  {
    path: 'login',
    loadComponent: () => import('./screens/login/login.component').then((m) => m.LoginComponent),
  },
  {
    path: 'app',
    canActivate: [authGuard],
    loadComponent: () =>
      import('./core/layout/workspace-shell.component').then((m) => m.WorkspaceShellComponent),
    children: [
      { path: '', redirectTo: 'dashboard', pathMatch: 'full' },
      {
        path: 'dashboard',
        loadComponent: () => import('./screens/dashboard/dashboard.component').then((m) => m.DashboardComponent),
      },
      {
        path: 'cases',
        loadComponent: () => import('./screens/cases/cases.component').then((m) => m.CasesComponent),
      },
      {
        path: 'cases/:caseId',
        loadComponent: () =>
          import('./screens/case-detail/case-detail.component').then((m) => m.CaseDetailComponent),
      },
      {
        path: 'cases/:caseId/documents/:documentId',
        loadComponent: () =>
          import('./screens/document-viewer/document-viewer.component').then((m) => m.DocumentViewerComponent),
      },
      {
        path: 'cases/:caseId/documents/:documentId/redact',
        loadComponent: () =>
          import('./screens/redact-studio/redact-studio.component').then((m) => m.RedactStudioComponent),
      },
      {
        path: 'shared',
        loadComponent: () =>
          import('./screens/shared-with-me/shared-with-me.component').then((m) => m.SharedWithMeComponent),
      },
      {
        path: 'access-preview',
        loadComponent: () =>
          import('./screens/access-preview/access-preview.component').then((m) => m.AccessPreviewComponent),
      },
      {
        path: 'audit',
        loadComponent: () => import('./screens/audit-log/audit-log.component').then((m) => m.AuditLogComponent),
      },
      {
        path: 'admin',
        canActivate: [adminGuard],
        loadComponent: () => import('./screens/admin/admin.component').then((m) => m.AdminComponent),
      },
    ],
  },
  {
    path: '**',
    redirectTo: 'landing',
  },
];
