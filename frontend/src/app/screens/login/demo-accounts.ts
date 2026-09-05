import { Role } from '../../core/models/api.models';

/**
 * Production stub — compiled as-is by the default (`production`) build
 * configuration. The real dev-only quick-sign-in credentials live in
 * `demo-accounts.development.ts`, swapped in for this file ONLY by the
 * `development` build configuration's `fileReplacements`
 * (../../../angular.json) — the same mechanism `environment.ts`/
 * `environment.development.ts` already use. This guarantees no demo
 * credential string is ever compiled into a production `dist/` output,
 * independent of `environment.demoMode`/the dynamic `import()` gating in
 * login.component.ts (which alone does not stop a bundler from emitting
 * an unreachable dynamic-import target as a fetchable static chunk).
 */
export interface DemoAccount {
  name: string;
  email: string;
  password: string;
  role: Role;
}

export const DEMO_ACCOUNTS: DemoAccount[] = [];
