import { Injectable, computed, inject, signal } from '@angular/core';
import { NavigationEnd, Router } from '@angular/router';
import { filter } from 'rxjs';
import { AuthService } from './auth.service';
import { DocumentService } from './document.service';
import { DocumentType, Role as BackendRole } from '../models/api.models';

export type Role = 'Police' | 'Judge' | 'Lawyer' | 'Forensics' | 'Admin';
export type Screen = 'landing' | 'login' | 'dash' | 'cases' | 'case' | 'doc' | 'audit' | 'redact' | 'access' | 'admin' | 'shared';

export interface NavItem {
  label: string;
  target: Screen | 'upload';
  icon: string;
  weight: number;
}

export interface StatItem {
  label: string;
  value: string;
  delta: string;
  deltaType: 'up' | 'down' | 'neutral' | 'warn';
  deltaColor: string;
}

export interface AuditRow {
  id: number;
  ts: string;
  user: string;
  role: string;
  action: string;
  resource: string;
  actionType: 'upload' | 'hash' | 'status' | 'view' | 'redact' | 'denied' | 'verify';
  hash: string;
  prev: string;
  ip: string;
  session: string;
  open?: boolean;
}

export interface ChainNode {
  id: string;
  frag: string;
  tick: string;
  verified: boolean;
  tampered: boolean;
  link: boolean;
  flex: string;
}

// Fixed demo hash used ONLY by the still-mock chain-verify sweep
// visualization below (chainRows) — the audit hash-chain itself remains
// unimplemented (see docs/AUDIT_CHAIN.md), so there is no real chain to
// display. Redaction is REAL — see document.service.ts's redact() and
// RedactStudioComponent, which display only server-returned hashes, never
// this constant. Document verification/certificates also use only real
// values — see document-viewer.component.ts.
export const H_RED = 'a09c73e51bd82f460a7e3c19d54b06f2837ea1c9b0d64f5382e17ca09bd435f6';

const BACKEND_TO_UI_ROLE: Record<BackendRole, Role> = {
  ADMIN: 'Admin',
  POLICE: 'Police',
  FORENSICS: 'Forensics',
  LAWYER: 'Lawyer',
  JUDGE: 'Judge',
};

/**
 * Shared UI state: navigation (now backed by the real Angular Router —
 * see the constructor and navigateTo()), role-derived nav/dashboard
 * content, and the upload-modal workflow (now a real
 * POST /cases/:id/documents call — see startUpload()).
 *
 * What is DELIBERATELY NOT here any more: authentication. Login/session
 * state lives entirely in AuthService (the real backend integration);
 * `currentUser`/`role` below are thin, template-compatible views DERIVED
 * from AuthService, never an independent source of truth — there is no
 * code path left that can set a role or user without a real
 * POST /auth/login response driving it (see docs/SECURITY.md — the
 * backend is authoritative for role regardless of what a client sends).
 *
 * Also gone: casesList/caseDocuments/caseTimeline/caseParties/
 * chainOfCustody, the hardcoded single-demo-case arrays the old mock UI
 * read from — CasesComponent/CaseDetailComponent/DocumentViewerComponent
 * now fetch real data directly from CaseService/DocumentService. Also gone
 * (System 8): adminUsers — AdminComponent now fetches real data from
 * AdminUserService (GET /admin/users). What REMAINS mock (dashboardInfo
 * stats, activityFeed, auditRows/chainRows, accessFields) has no backend
 * equivalent yet (dashboard stats, audit-log read, and chain verification
 * are not implemented by any system yet — see ARCHITECTURE.md) and is
 * left as clearly-illustrative content, per master prompt's explicit
 * "do not implement functionality belonging to Systems 8+".
 */
@Injectable({
  providedIn: 'root'
})
export class DmsStateService {
  private readonly router = inject(Router);
  private readonly auth = inject(AuthService);
  private readonly documentService = inject(DocumentService);

  // ---- Navigation (Router-backed) ----
  private readonly _screen = signal<Screen>(this.mapUrlToScreen(this.router.url));
  readonly screen = this._screen.asReadonly();

  private readonly screenPaths: Partial<Record<Screen, string>> = {
    landing: '/landing',
    login: '/login',
    dash: '/app/dashboard',
    cases: '/app/cases',
    audit: '/app/audit',
    access: '/app/access-preview',
    admin: '/app/admin',
    shared: '/app/shared',
  };

  constructor() {
    this.router.events.pipe(filter((e): e is NavigationEnd => e instanceof NavigationEnd)).subscribe((e) => {
      this._screen.set(this.mapUrlToScreen(e.urlAfterRedirects));
    });
  }

  private mapUrlToScreen(url: string): Screen {
    const path = url.split('?')[0];
    if (path.startsWith('/login')) return 'login';
    if (!path.startsWith('/app')) return 'landing';

    const segs = path.replace(/^\/app\/?/, '').split('/').filter(Boolean);
    if (segs.length === 0 || segs[0] === 'dashboard') return 'dash';
    if (segs[0] === 'audit') return 'audit';
    if (segs[0] === 'admin') return 'admin';
    if (segs[0] === 'shared') return 'shared';
    if (segs[0] === 'access-preview') return 'access';
    if (segs[0] === 'cases') {
      if (segs.length <= 1) return 'cases';
      if (segs.length >= 5 && segs[4] === 'redact') return 'redact';
      if (segs.length >= 4 && segs[2] === 'documents') return 'doc';
      return 'case';
    }
    return 'dash';
  }

  /** Navigates for the screens that need no dynamic ID (dashboard, cases
   * list, audit, admin, access-preview, login, landing). Case/document
   * detail navigation happens directly via Router in the component that
   * already has the real ID (CasesComponent.openCase,
   * CaseDetailComponent.openDoc, etc.) — see those files. */
  navigateTo(target: Screen | 'upload') {
    if (target === 'upload') {
      // No case context from a generic nav click — send the user to pick
      // one; a specific case's own "Upload Document" button
      // (CaseDetailComponent) opens the modal directly with that case's
      // id via openUploadModal(caseId).
      this.router.navigateByUrl('/app/cases');
      return;
    }
    const path = this.screenPaths[target];
    if (path) this.router.navigateByUrl(path);
  }

  // ---- Authenticated user (derived from AuthService — see class doc) ----
  readonly role = computed<Role>(() => {
    const backendRole = this.auth.role();
    return backendRole ? BACKEND_TO_UI_ROLE[backendRole] : 'Police';
  });

  readonly currentUser = computed(() => {
    const u = this.auth.currentUser();
    if (!u) return null;
    const name = `${u.first_name} ${u.last_name}`.trim();
    const initials = `${u.first_name.charAt(0)}${u.last_name.charAt(0)}`.toUpperCase();
    return { name, initials, email: u.email, role: BACKEND_TO_UI_ROLE[u.role] };
  });

  signOut() {
    this.auth.logout().subscribe(() => this.router.navigateByUrl('/login'));
  }

  // Legacy demo-only chain-tamper visual flag — no toggle exists in the
  // UI any more (removing the header's toggle was necessary: a
  // client-side flag with no relationship to a real backend check would
  // otherwise sit right next to the now-REAL "Verify Integrity" action
  // and look like it does something). Always false; kept only so the
  // still-mock audit-log/dashboard/sidebar chain-status widgets (System
  // 8's audit-chain verification is not implemented by any system
  // through 7) keep compiling unchanged.
  readonly simulateTamper = signal<boolean>(false);

  readonly roles: Role[] = ['Police', 'Judge', 'Lawyer', 'Forensics', 'Admin'];

  // Breadcrumb mapping — generic per screen type; no longer names a
  // specific fake case/document (the old text hardcoded "FIR/2026/4521"
  // regardless of which real case/document was actually open).
  readonly breadcrumb = computed(() => {
    const s = this.screen();
    const map: Record<Screen, string> = {
      landing: 'Welcome / Home',
      login: 'Sign In',
      dash: 'Home / Dashboard',
      cases: 'Home / Cases',
      case: 'Home / Cases / Case Detail',
      doc: 'Home / Cases / Case Detail / Document',
      redact: 'Home / Cases / Case Detail / Document / Redact',
      audit: 'Home / Audit Log',
      access: 'Home / Access Policy Preview',
      admin: 'Home / Administration / Users',
      shared: 'Home / Shared With Me'
    };
    return map[s] || 'Home';
  });

  // Navigation items per role
  readonly navItems = computed<NavItem[]>(() => {
    const r = this.role();
    const map: Record<Role, string[]> = {
      Police: ['Dashboard', 'Cases', 'Upload Document', 'Shared With Me', 'Audit Log'],
      Judge: ['Dashboard', 'Cases', 'Shared With Me', 'Audit Log'],
      Lawyer: ['Dashboard', 'Cases', 'Shared With Me'],
      Forensics: ['Dashboard', 'Cases', 'Upload Document', 'Shared With Me'],
      Admin: ['Dashboard', 'Cases', 'Upload Document', 'Shared With Me', 'Audit Log', 'User Management']
    };
    const list = map[r] || map.Police;

    const screenTargetMap: Record<string, Screen | 'upload'> = {
      'Dashboard': 'dash',
      'Cases': 'cases',
      'Upload Document': 'upload',
      'Shared With Me': 'shared',
      'Audit Log': 'audit',
      'User Management': 'admin'
    };

    const iconMap: Record<string, string> = {
      'Dashboard': 'dashboard',
      'Cases': 'folder',
      'Upload Document': 'upload',
      'Shared With Me': 'share',
      'Audit Log': 'shield',
      'User Management': 'users'
    };

    return list.map(label => ({
      label,
      target: screenTargetMap[label],
      icon: iconMap[label] || 'circle',
      weight: 500
    }));
  });

  // Dashboard configuration by role — illustrative only; no backend
  // endpoint provides aggregate stats for any system through 7.
  readonly dashboardInfo = computed(() => {
    const r = this.role();
    switch (r) {
      case 'Judge':
        return {
          title: 'Judicial Docket',
          sub: 'Matters listed before Sessions Court 04 this fortnight.',
          stats: [
            { label: 'Cases on docket', value: '23', delta: '4 listed today', deltaType: 'neutral', deltaColor: '#426d9b' },
            { label: 'Hearings this week', value: '6', delta: 'next 11:30 IST', deltaType: 'neutral', deltaColor: '#5b6775' },
            { label: 'Flagged integrity', value: '1', delta: 'review required', deltaType: 'warn', deltaColor: '#c53030' },
            { label: 'Orders pending', value: '2', delta: 'due 3 Mar', deltaType: 'warn', deltaColor: '#b26a00' }
          ]
        };
      case 'Lawyer':
        return {
          title: 'Legal Matters & Disclosures',
          sub: 'Cases you are attached to as defence / public counsel.',
          stats: [
            { label: 'Active matters', value: '9', delta: '+1 this week', deltaType: 'up', deltaColor: '#2e7d4f' },
            { label: 'Shared with you', value: '34', delta: '5 new documents', deltaType: 'up', deltaColor: '#2e7d4f' },
            { label: 'Disclosure deadlines', value: '2', delta: 'earliest 2 Mar', deltaType: 'warn', deltaColor: '#b26a00' },
            { label: 'Restricted items', value: '11', delta: 'role-limited (§207)', deltaType: 'neutral', deltaColor: '#5b6775' }
          ]
        };
      case 'Forensics':
        return {
          title: 'Forensic Analysis Queue',
          sub: 'Evidence requests routed to State Cyber Forensics Laboratory.',
          stats: [
            { label: 'Pending analyses', value: '7', delta: '2 high priority', deltaType: 'warn', deltaColor: '#b26a00' },
            { label: 'Reports submitted', value: '41', delta: '3 this week', deltaType: 'up', deltaColor: '#2e7d4f' },
            { label: 'Exhibits in custody', value: '19', delta: 'all tamper-sealed', deltaType: 'up', deltaColor: '#2e7d4f' },
            { label: 'Chain breaks', value: '0', delta: 'verified 14:02 IST', deltaType: 'up', deltaColor: '#2e7d4f' }
          ]
        };
      case 'Admin':
        return {
          title: 'System Administration',
          sub: 'Users, cryptographic key management, and platform audit trail.',
          stats: [
            { label: 'Active personnel', value: '86', delta: '+4 this month', deltaType: 'up', deltaColor: '#2e7d4f' },
            { label: 'Total cases registered', value: '412', delta: 'across 14 stations', deltaType: 'neutral', deltaColor: '#5b6775' },
            { label: 'Audit entries', value: '1,204', delta: 'append-only verified', deltaType: 'up', deltaColor: '#2e7d4f' },
            { label: 'Access anomalies', value: '0', delta: 'last 7 days', deltaType: 'up', deltaColor: '#2e7d4f' }
          ]
        };
      case 'Police':
      default:
        return {
          title: 'Investigation Desk',
          sub: 'Cases assigned to you at Noida Sector 58 Police Station.',
          stats: [
            { label: 'Open cases', value: '12', delta: '+2 this week', deltaType: 'up', deltaColor: '#2e7d4f' },
            { label: 'Documents uploaded', value: '148', delta: '9 in last 24h', deltaType: 'up', deltaColor: '#426d9b' },
            { label: 'Pending redactions', value: '3', delta: '1 overdue (§228A)', deltaType: 'warn', deltaColor: '#b26a00' },
            { label: 'Integrity alerts', value: '0', delta: 'chain intact', deltaType: 'up', deltaColor: '#2e7d4f' }
          ]
        };
    }
  });

  // Recent Activity Feed — illustrative only, no backend timeline/activity
  // feed endpoint exists yet.
  readonly activityFeed = [
    { text: 'Forensic_Report_UPI_4521.pdf uploaded', meta: 'sha256 d41f9a3c…2d58 · Dr. A. Iyer', time: '2h ago', dotColor: '#2e7d4f' },
    { text: 'Adv. S. Bhat viewed Witness_statement_02.pdf', meta: 'audit entry #1203 · Disclosure verified', time: '4h ago', dotColor: '#426d9b' },
    { text: 'Redacted copy created — Witness_statement_02.pdf', meta: 'sha256 a09c73e5…35f6 · §228A compliance', time: 'Yesterday', dotColor: '#b26a00' },
    { text: 'Chain integrity verified — 1,204 entries intact', meta: 'automated system daemon · ED25519-01', time: 'Yesterday', dotColor: '#2e7d4f' },
    { text: 'Access denied — defence counsel, exhibit E-04', meta: 'policy enforcement: police-only clearance', time: '2 days ago', dotColor: '#c53030' }
  ];

  // Access Policy Preview Fields — static illustrative content
  // (access-preview screen isn't backed by any specific endpoint).
  readonly accessFields = [
    { label: 'Complainant Full Name', value: 'Meera Krishnan', restricted: false },
    { label: 'Complainant Contact', value: '+91 98•• ••4471', restricted: false },
    { label: 'Witness Protected Identity', value: 'Suresh Pillai, 44, Sector 62', restricted: true },
    { label: 'Seized Mobile IMEI', value: '35•••••••••••21', restricted: false },
    { label: 'Beneficiary Bank Account', value: 'HDFC ••••7742 — Kolkata Gariahat branch', restricted: false },
    { label: 'Investigating Officer Field Diary', value: 'Suspect vehicle registration DL-3C-AZ-9912 traced via toll gantry camera', restricted: true }
  ];

  // Audit Rows — illustrative only; no GET /audit endpoint is implemented
  // by any system through 7 (audit_log is written to operationally but
  // has no read API yet — see docs/AUDIT_CHAIN.md).
  readonly auditRows: AuditRow[] = [
    { id: 1, ts: '18 Feb 10:14:07', user: 'Dr. A. Iyer', role: 'Forensics', action: 'DOCUMENT_UPLOAD', resource: 'Forensic_Report_UPI_4521.pdf', actionType: 'upload', hash: 'd41f9a3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58', prev: '7b2e4c91a08df365c4a17e0b92d5f83a6c1e074bd39f52a8e6c0b74132fd9e05', ip: '10.14.6.21', session: 's-8107' },
    { id: 2, ts: '18 Feb 10:14:09', user: 'system', role: 'System', action: 'HASH_RECORDED', resource: 'sha256:d41f9a3c…', actionType: 'hash', hash: '1f9a3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d4', prev: 'd41f9a3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58', ip: '127.0.0.1', session: 'daemon-sys' },
    { id: 3, ts: '18 Feb 11:02:41', user: 'SI R. Mehra', role: 'Police', action: 'CASE_STATUS_CHANGE', resource: 'FIR/2026/4521', actionType: 'status', hash: '9a3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f', prev: '1f9a3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d4', ip: '10.14.6.22', session: 's-8114' },
    { id: 4, ts: '18 Feb 12:33:18', user: 'Adv. S. Bhat', role: 'Lawyer', action: 'DOCUMENT_VIEW', resource: 'Witness_statement_02.pdf', actionType: 'view', hash: '3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a', prev: '9a3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f', ip: '10.14.6.23', session: 's-8121' },
    { id: 5, ts: '18 Feb 12:41:55', user: 'SI R. Mehra', role: 'Police', action: 'REDACTION_APPLIED', resource: 'Witness_statement_02.pdf', actionType: 'redact', hash: '7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a3c', prev: '3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a', ip: '10.14.6.24', session: 's-8128' },
    { id: 6, ts: '18 Feb 13:07:02', user: 'Adv. M. Qureshi', role: 'Lawyer', action: 'ACCESS_DENIED', resource: 'Exhibit E-04 (police-only)', actionType: 'denied', hash: '208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a3c7b', prev: '7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a3c', ip: '10.14.6.25', session: 's-8135' },
    { id: 7, ts: '18 Feb 13:58:20', user: 'Hon. K. Mahadevan', role: 'Judge', action: 'DOCUMENT_VIEW', resource: 'FIR_4521_scan.pdf', actionType: 'view', hash: '8e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a3c7b20', prev: '208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a3c7b', ip: '10.14.6.26', session: 's-8142' },
    { id: 8, ts: '18 Feb 14:02:00', user: 'system', role: 'System', action: 'CHAIN_VERIFY', resource: '1,204 entries — intact', actionType: 'verify', hash: '5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a3c7b208e', prev: '8e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58d41f9a3c7b20', ip: '127.0.0.1', session: 'daemon-sys' }
  ];

  // Chain Verification State — illustrative sweep animation; no
  // audit-chain verification endpoint exists yet (System 8's scope — see
  // docs/AUDIT_CHAIN.md).
  readonly chainRunning = signal<boolean>(false);
  readonly chainDone = signal<boolean>(false);
  readonly chainCount = signal<number>(0);
  readonly auditTab = signal<'table' | 'graph'>('table');
  readonly expandedAuditId = signal<number | null>(null);

  readonly chainRows = computed(() => {
    const swept = Math.round((this.chainCount() / 1204) * 24);
    const isDone = this.chainDone();
    const isRunning = this.chainRunning();
    const tamper = this.simulateTamper();

    const nodes: ChainNode[] = Array.from({ length: 24 }, (_, i) => {
      const on = isDone || (isRunning && i < swept);
      const broken = tamper && isDone && i === 17;
      return {
        id: '#' + (1181 + i),
        frag: H_RED.slice(i, i + 4),
        tick: broken ? '×' : (on ? '✓' : ''),
        verified: on && !broken,
        tampered: broken,
        link: i % 8 !== 7,
        flex: i % 8 === 7 ? '0 0 auto' : '1'
      };
    });

    return [
      { nodes: nodes.slice(0, 8) },
      { nodes: nodes.slice(8, 16) },
      { nodes: nodes.slice(16, 24) }
    ];
  });

  verifyChain() {
    this.chainRunning.set(true);
    this.chainDone.set(false);
    this.chainCount.set(0);

    const timerId = setInterval(() => {
      const next = Math.min(1204, this.chainCount() + 48);
      this.chainCount.set(next);

      if (next >= 1204) {
        clearInterval(timerId);
        setTimeout(() => {
          this.chainRunning.set(false);
          this.chainDone.set(true);
        }, 200);
      }
    }, 45);

    this.activeTimers.push(timerId);
  }

  toggleAuditRow(id: number) {
    this.expandedAuditId.set(this.expandedAuditId() === id ? null : id);
  }

  // ---- Upload Modal (REAL — POST /cases/:id/documents) ----
  readonly uploadOpen = signal<boolean>(false);
  readonly uploadCaseId = signal<string | null>(null);
  readonly uploadPhase = signal<'idle' | 'progress' | 'hashing' | 'done'>('idle');
  readonly uploadPct = signal<number>(0);
  readonly liveHash = signal<string>('');
  readonly uploadError = signal<string | null>(null);
  /** Bumped to Date.now() after a successful upload — components showing
   * a case's document list (CaseDetailComponent) watch this via effect()
   * to know when to refetch, since the modal that produced the upload
   * lives outside their own component tree (WorkspaceShellComponent). */
  readonly documentUploaded = signal<number>(0);

  openUploadModal(caseId: string) {
    this.uploadCaseId.set(caseId);
    this.uploadOpen.set(true);
    this.uploadPhase.set('idle');
    this.uploadPct.set(0);
    this.liveHash.set('');
    this.uploadError.set(null);
  }

  closeUploadModal() {
    this.uploadOpen.set(false);
    this.uploadPhase.set('idle');
    this.clearTimers();
  }

  /** Performs the real multipart upload, driving uploadPct from actual
   * browser upload-progress events (see ApiClientService.postMultipart) —
   * never a fake timer. On success, liveHash is the real sha256_hash the
   * backend computed from the uploaded bytes, not a client-side guess. */
  startUpload(file: File, documentType: DocumentType, description: string) {
    const caseId = this.uploadCaseId();
    if (!caseId) return;

    this.uploadPhase.set('progress');
    this.uploadPct.set(0);
    this.uploadError.set(null);

    this.documentService.upload(caseId, { documentType, description: description || undefined, file }).subscribe({
      next: (event) => {
        if (event.type === 'progress') {
          this.uploadPct.set(event.percent);
          if (event.percent >= 100) this.uploadPhase.set('hashing');
        } else {
          this.liveHash.set(event.data.sha256_hash);
          this.uploadPhase.set('done');
          this.documentUploaded.set(Date.now());
        }
      },
      error: (err) => {
        this.uploadPhase.set('idle');
        this.uploadError.set(err?.message ?? 'Upload failed. Please try again.');
      }
    });
  }

  // Timers for the still-mock chain-verify sweep animation above.
  private activeTimers: any[] = [];
  private clearTimers() {
    this.activeTimers.forEach(t => clearInterval(t));
    this.activeTimers = [];
  }
}
