import { Routes } from '@angular/router';

export const routes: Routes = [
  {
    path: '',
    redirectTo: 'landing',
    pathMatch: 'full'
  },
  {
    path: 'landing',
    loadComponent: () => import('./screens/landing-page/landing-page.component').then(m => m.LandingPageComponent)
  },
  {
    path: '**',
    redirectTo: 'landing'
  }
];
