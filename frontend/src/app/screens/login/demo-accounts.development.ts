import { Role } from '../../core/models/api.models';

/**
 * Local-development-only convenience data — dev accounts you create
 * yourself with backend/cmd/devuser (never real credentials, nothing
 * committed here is a production secret).
 *
 * This file is swapped in ONLY for the `development` build configuration
 * (see ../../../angular.json's `fileReplacements`, the same mechanism
 * `environment.ts`/`environment.development.ts` already use) — a
 * production build compiles `demo-accounts.ts` (the empty stub) instead,
 * so these strings are never present in a production `dist/` output at
 * all, regardless of whether `environment.demoMode`/the dynamic `import()`
 * gating login.component.ts also uses would otherwise be dead-code-
 * eliminated by the bundler. Do not rely on that gating alone — a dynamic
 * `import()` still causes a bundler to emit its target as a fetchable
 * static chunk even when the branch that triggers it never runs, which is
 * exactly why this file-replacement swap, not just the runtime check, is
 * the actual security boundary here.
 */
export interface DemoAccount {
  name: string;
  email: string;
  password: string;
  role: Role;
}

export const DEMO_ACCOUNTS: DemoAccount[] = [
  {
    name: 'SI Rajat Mehra',
    email: 'police@delhipolice.gov.in',
    password: 'police123',
    role: 'POLICE',
  },
  {
    name: 'Hon. K. Mahadevan',
    email: 'judge@ecourts.gov.in',
    password: 'judge12345',
    role: 'JUDGE',
  },
  {
    name: 'Shalini Bhat',
    email: 'lawyer@prosecution.gov.in',
    password: 'lawyer1234',
    role: 'LAWYER',
  },
  {
    name: 'Dr. Anjali Iyer',
    email: 'forensics@cyberlab.gov.in',
    password: 'forensic123',
    role: 'FORENSICS',
  },
  { name: 'Nikhil Rao', email: 'admin@ncrb.gov.in', password: 'admin1234', role: 'ADMIN' },
];
