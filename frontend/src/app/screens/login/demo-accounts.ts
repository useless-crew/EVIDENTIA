import { Role } from '../../core/models/api.models';

/**
 * Local-development-only convenience data — dev accounts you create
 * yourself with backend/cmd/devuser (never real credentials, nothing
 * committed here is a production secret). Deliberately isolated in its
 * own file and reached ONLY via a dynamic import gated on
 * environment.demoMode (see login.component.ts) so this module — and the
 * strings in it — is a separate chunk a production build (demoMode false)
 * never imports and the browser never fetches, not merely a hidden UI
 * element still shipped in the main bundle.
 */
export interface DemoAccount {
  name: string;
  email: string;
  password: string;
  role: Role;
}

export const DEMO_ACCOUNTS: DemoAccount[] = [
  { name: 'SI Rajat Mehra', email: 'police@delhipolice.gov.in', password: 'police123', role: 'POLICE' },
  { name: 'Hon. K. Mahadevan', email: 'judge@ecourts.gov.in', password: 'judge12345', role: 'JUDGE' },
  { name: 'Shalini Bhat', email: 'lawyer@prosecution.gov.in', password: 'lawyer1234', role: 'LAWYER' },
  { name: 'Dr. Anjali Iyer', email: 'forensics@cyberlab.gov.in', password: 'forensic123', role: 'FORENSICS' },
  { name: 'Nikhil Rao', email: 'admin@ncrb.gov.in', password: 'admin1234', role: 'ADMIN' },
];
