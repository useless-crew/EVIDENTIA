import { Injectable, signal, computed } from '@angular/core';

export type Role = 'Police' | 'Judge' | 'Lawyer' | 'Forensics' | 'Admin';
export type Screen = 'landing' | 'login' | 'dash' | 'cases' | 'case' | 'doc' | 'audit' | 'redact' | 'access' | 'admin';

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

export interface CaseRecord {
  no: string;
  title: string;
  status: 'Under investigation' | 'In trial' | 'Chargesheet filed' | 'Closed';
  by: string;
  updated: string;
  docs: number;
}

export interface DocItem {
  name: string;
  kind: string;
  badge: 'Verified' | 'Pending review' | 'Redacted copy' | 'Hash mismatch';
  badgeType: 'verified' | 'pending' | 'redacted' | 'mismatch';
  by: string;
}

export interface TimelineItem {
  text: string;
  time: string;
  isLatest: boolean;
}

export interface PartyItem {
  initials: string;
  name: string;
  role: string;
  accent: string;
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

export interface RedactionRegion {
  id: number;
  x: number;
  y: number;
  w: number;
  h: number;
  dims: string;
  reason: string;
}

export interface UserAccount {
  name: string;
  email: string;
  password: string;
  role: Role;
  agency: string;
  initials: string;
}

export const DUMMY_ACCOUNTS: UserAccount[] = [
  {
    name: 'SI Rajat Mehra',
    email: 'police@delhipolice.gov.in',
    password: 'police123',
    role: 'Police',
    agency: 'Noida Sec 58 Police Station',
    initials: 'RM'
  },
  {
    name: 'Hon. K. Mahadevan',
    email: 'judge@ecourts.gov.in',
    password: 'judge123',
    role: 'Judge',
    agency: 'Sessions Court 04',
    initials: 'KM'
  },
  {
    name: 'Shalini Bhat',
    email: 'lawyer@prosecution.gov.in',
    password: 'lawyer123',
    role: 'Lawyer',
    agency: 'District Prosecution Branch',
    initials: 'SB'
  },
  {
    name: 'Dr. Anjali Iyer',
    email: 'forensics@cyberlab.gov.in',
    password: 'forensic123',
    role: 'Forensics',
    agency: 'State Cyber Forensics Lab',
    initials: 'AI'
  },
  {
    name: 'Nikhil Rao',
    email: 'admin@ncrb.gov.in',
    password: 'admin123',
    role: 'Admin',
    agency: 'National Crime Records Bureau',
    initials: 'NR'
  }
];

const HEX_CHARS = '0123456789abcdef';
export const H_DOC = 'd41f9a3c7b208e5641c0ba97e3f5d2a80c6b491e7fa3d5c28b0e1947fc63a2d58';
export const H_UP  = '7b2e4c91a08df365c4a17e0b92d5f83a6c1e074bd39f52a8e6c0b74132fd9e05';
export const H_RED = 'a09c73e51bd82f460a7e3c19d54b06f2837ea1c9b0d64f5382e17ca09bd435f6';

@Injectable({
  providedIn: 'root'
})
export class DmsStateService {
  // Navigation & Role State
  readonly screen = signal<Screen>('landing');
  readonly role = signal<Role>('Police');
  readonly roles: Role[] = ['Police', 'Judge', 'Lawyer', 'Forensics', 'Admin'];
  readonly simulateTamper = signal<boolean>(false);

  // Authenticated User & JWT Session State
  readonly currentUser = signal<UserAccount | null>(DUMMY_ACCOUNTS[0]);
  readonly jwtToken = signal<string | null>(null);

  constructor() {
    this.restoreSession();
  }

  restoreSession() {
    try {
      if (typeof window !== 'undefined' && window.localStorage) {
        const saved = localStorage.getItem('evidentia_session');
        if (saved) {
          const data = JSON.parse(saved);
          if (data && data.user && data.token) {
            this.currentUser.set(data.user);
            this.jwtToken.set(data.token);
            this.role.set(data.user.role);
          }
        }
      }
    } catch {}
  }

  generateJwtToken(account: UserAccount): string {
    const header = btoa(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
    const payload = btoa(JSON.stringify({
      sub: account.email,
      name: account.name,
      role: account.role,
      agency: account.agency,
      iat: Math.floor(Date.now() / 1000),
      exp: Math.floor(Date.now() / 1000) + 86400
    }));
    const sig = 'c3VwZXJzZWNyZXRqd3RzaWduYXR1cmU';
    return `${header}.${payload}.${sig}`;
  }

  loginWithAccount(account: UserAccount): boolean {
    const token = this.generateJwtToken(account);
    this.currentUser.set(account);
    this.jwtToken.set(token);
    this.role.set(account.role);
    this.screen.set('dash');

    try {
      if (typeof window !== 'undefined' && window.localStorage) {
        localStorage.setItem('evidentia_session', JSON.stringify({ token, user: account }));
      }
    } catch {}

    return true;
  }

  loginWithCredentials(emailInput: string, passwordInput: string): { success: boolean; message?: string } {
    const normalizedEmail = emailInput.trim().toLowerCase();

    // Check exact dummy account match
    let found = DUMMY_ACCOUNTS.find(
      a => a.email.toLowerCase() === normalizedEmail && (a.password === passwordInput || passwordInput.length >= 3)
    );

    // Fallback: match by role keyword or domain prefix
    if (!found) {
      if (normalizedEmail.includes('judge') || normalizedEmail.includes('ecourts')) {
        found = DUMMY_ACCOUNTS.find(a => a.role === 'Judge');
      } else if (normalizedEmail.includes('lawyer') || normalizedEmail.includes('prosecution')) {
        found = DUMMY_ACCOUNTS.find(a => a.role === 'Lawyer');
      } else if (normalizedEmail.includes('forensic') || normalizedEmail.includes('lab')) {
        found = DUMMY_ACCOUNTS.find(a => a.role === 'Forensics');
      } else if (normalizedEmail.includes('admin') || normalizedEmail.includes('ncrb')) {
        found = DUMMY_ACCOUNTS.find(a => a.role === 'Admin');
      } else {
        found = DUMMY_ACCOUNTS.find(a => a.role === 'Police');
      }
    }

    if (found) {
      this.loginWithAccount(found);
      return { success: true };
    }

    return { success: false, message: 'Invalid government credentials.' };
  }

  signOut() {
    this.currentUser.set(null);
    this.jwtToken.set(null);
    try {
      if (typeof window !== 'undefined' && window.localStorage) {
        localStorage.removeItem('evidentia_session');
      }
    } catch {}
    this.screen.set('login');
  }

  // Upload Modal State
  readonly uploadOpen = signal<boolean>(false);
  readonly uploadPhase = signal<'idle' | 'progress' | 'hashing' | 'done'>('idle');
  readonly uploadPct = signal<number>(0);
  readonly liveHash = signal<string>('');

  // Document Integrity State
  readonly verifyState = signal<'idle' | 'checking' | 'ok' | 'tampered'>('idle');
  readonly certOpen = signal<boolean>(false);

  // Chain Verification State
  readonly chainRunning = signal<boolean>(false);
  readonly chainDone = signal<boolean>(false);
  readonly chainCount = signal<number>(0);
  readonly auditTab = signal<'table' | 'graph'>('table');
  readonly expandedAuditId = signal<number | null>(null);

  // Redaction Canvas State
  readonly redactions = signal<RedactionRegion[]>([]);
  readonly draft = signal<{ x: number; y: number; w: number; h: number } | null>(null);
  readonly redactSaved = signal<boolean>(false);

  // Timers for animations
  private activeTimers: any[] = [];

  // Breadcrumb mapping
  readonly breadcrumb = computed(() => {
    const s = this.screen();
    const map: Record<Screen, string> = {
      landing: 'Welcome / Home',
      login: 'Sign In',
      dash: 'Home / Dashboard',
      cases: 'Home / Cases',
      case: 'Home / Cases / FIR/2026/4521',
      doc: 'Home / Cases / FIR/2026/4521 / Forensic_Report_UPI_4521.pdf',
      redact: 'Home / Cases / FIR/2026/4521 / Document / Redact',
      audit: 'Home / Audit Log',
      access: 'Home / Cases / FIR/2026/4521 / Access Preview',
      admin: 'Home / Administration / Users'
    };
    return map[s] || 'Home';
  });

  // Navigation items per role
  readonly navItems = computed<NavItem[]>(() => {
    const r = this.role();
    const map: Record<Role, string[]> = {
      Police: ['Dashboard', 'Cases', 'Upload Document', 'Audit Log'],
      Judge: ['Dashboard', 'Cases', 'Audit Log'],
      Lawyer: ['Dashboard', 'Cases'],
      Forensics: ['Dashboard', 'Cases', 'Upload Document'],
      Admin: ['Dashboard', 'Cases', 'Upload Document', 'Audit Log', 'User Management']
    };
    const list = map[r] || map.Police;

    const screenTargetMap: Record<string, Screen | 'upload'> = {
      'Dashboard': 'dash',
      'Cases': 'cases',
      'Upload Document': 'upload',
      'Audit Log': 'audit',
      'User Management': 'admin'
    };

    const iconMap: Record<string, string> = {
      'Dashboard': 'dashboard',
      'Cases': 'folder',
      'Upload Document': 'upload',
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

  // Dashboard configuration by role
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

  // Recent Activity Feed
  readonly activityFeed = [
    { text: 'Forensic_Report_UPI_4521.pdf uploaded', meta: 'sha256 d41f9a3c…2d58 · Dr. A. Iyer', time: '2h ago', dotColor: '#2e7d4f' },
    { text: 'Adv. S. Bhat viewed Witness_statement_02.pdf', meta: 'audit entry #1203 · Disclosure verified', time: '4h ago', dotColor: '#426d9b' },
    { text: 'Redacted copy created — Witness_statement_02.pdf', meta: 'sha256 a09c73e5…35f6 · §228A compliance', time: 'Yesterday', dotColor: '#b26a00' },
    { text: 'Chain integrity verified — 1,204 entries intact', meta: 'automated system daemon · ED25519-01', time: 'Yesterday', dotColor: '#2e7d4f' },
    { text: 'Access denied — defence counsel, exhibit E-04', meta: 'policy enforcement: police-only clearance', time: '2 days ago', dotColor: '#c53030' }
  ];

  // Cases List Data
  readonly casesList: CaseRecord[] = [
    { no: 'FIR/2026/4521', title: 'Cyber fraud — unauthorised UPI transfers, Sector 62', status: 'Under investigation', by: 'SI R. Mehra', updated: '2h ago', docs: 7 },
    { no: 'FIR/2026/4488', title: 'Vehicle theft — Kalindi Kunj parking complex', status: 'Chargesheet filed', by: 'SI D. Rana', updated: 'Yesterday', docs: 12 },
    { no: 'CC/2025/1189', title: 'State v. Nagpal — cheque dishonour & extortion', status: 'In trial', by: 'PP S. Bhat', updated: '3 days ago', docs: 24 },
    { no: 'FIR/2026/4502', title: 'Data exfiltration at private hospital pathology server', status: 'Under investigation', by: 'Insp. K. Verma', updated: '5h ago', docs: 9 },
    { no: 'FIR/2025/3871', title: 'Counterfeit currency seizure — Anand Vihar ISBT', status: 'Closed', by: 'SI R. Mehra', updated: '12 Jan 2026', docs: 31 },
    { no: 'CC/2026/0042', title: 'State v. Qureshi — commercial narcotics possession', status: 'In trial', by: 'PP L. Nair', updated: '1 day ago', docs: 18 }
  ];

  // Case Documents
  readonly caseDocuments: DocItem[] = [
    { name: 'FIR_4521_scan.pdf', kind: 'PDF · 6pp', badge: 'Verified', badgeType: 'verified', by: 'SI R. Mehra' },
    { name: 'Forensic_Report_UPI_4521.pdf', kind: 'PDF · 22pp', badge: 'Verified', badgeType: 'verified', by: 'Dr. A. Iyer' },
    { name: 'Bank_statement_HDFC.pdf', kind: 'PDF · 4pp', badge: 'Verified', badgeType: 'verified', by: 'SI R. Mehra' },
    { name: 'Seizure_memo_device.jpg', kind: 'JPG · 1.1 MB', badge: 'Pending review', badgeType: 'pending', by: 'HC P. Singh' },
    { name: 'Witness_statement_02.pdf', kind: 'PDF · 3pp', badge: 'Redacted copy', badgeType: 'redacted', by: 'SI R. Mehra' },
    { name: 'CCTV_frame_export.png', kind: 'PNG · 0.8 MB', badge: 'Hash mismatch', badgeType: 'mismatch', by: 'HC P. Singh' }
  ];

  // Case Timeline
  readonly caseTimeline: TimelineItem[] = [
    { text: 'Case registered under Bharatiya Nyaya Sanhita §318(4)', time: '11 Feb 2026 · 09:12 IST', isLatest: false },
    { text: 'Mobile device seized and sealed — exhibit E-04', time: '11 Feb 2026 · 17:40 IST', isLatest: false },
    { text: 'Forensic imaging requisition raised with Cyber Lab', time: '13 Feb 2026 · 10:05 IST', isLatest: false },
    { text: 'Forensic report uploaded and signed by Dr. A. Iyer', time: '18 Feb 2026 · 10:14 IST', isLatest: false },
    { text: 'Status updated to Under Investigation with bank log additions', time: '18 Feb 2026 · 11:02 IST', isLatest: true }
  ];

  // Case Parties
  readonly caseParties: PartyItem[] = [
    { initials: 'RM', name: 'SI Rajat Mehra', role: 'Investigating Officer (IO)', accent: '#132a49' },
    { initials: 'AI', name: 'Dr. Anjali Iyer', role: 'Forensics — State Cyber Lab', accent: '#2e7d4f' },
    { initials: 'SB', name: 'Shalini Bhat', role: 'Public Prosecutor', accent: '#426d9b' },
    { initials: 'KM', name: 'Hon. K. Mahadevan', role: 'Presiding Judge, Sessions Court', accent: '#b26a00' }
  ];

  // Chain of Custody for Document
  readonly chainOfCustody = [
    { text: 'Uploaded & fingerprinted by Dr. A. Iyer', hash: 'd41f9a3c…2d58', dotColor: '#2e7d4f' },
    { text: 'Cryptographic receipt issued & signed by System', hash: '3c7b208e…1e94', dotColor: '#426d9b' },
    { text: 'Inspected by SI Rajat Mehra (Noida Sec 58 PS)', hash: 'b0e19470…c63a', dotColor: '#5b6775' },
    { text: 'Prosecution disclosure bundle attached by Adv. Bhat', hash: '7fa3d5c2…8b0e', dotColor: '#5b6775' },
    { text: 'Sealed for judicial trial record by Sessions Registrar', hash: 'c0ba97e3…f5d2', dotColor: '#132a49' }
  ];

  // Audit Rows
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

  // Blockchain Graph Nodes
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
        frag: H_DOC.slice(i, i + 4),
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

  // Access Policy Preview Fields
  readonly accessFields = [
    { label: 'Complainant Full Name', value: 'Meera Krishnan', restricted: false },
    { label: 'Complainant Contact', value: '+91 98•• ••4471', restricted: false },
    { label: 'Witness Protected Identity', value: 'Suresh Pillai, 44, Sector 62', restricted: true },
    { label: 'Seized Mobile IMEI', value: '35•••••••••••21', restricted: false },
    { label: 'Beneficiary Bank Account', value: 'HDFC ••••7742 — Kolkata Gariahat branch', restricted: false },
    { label: 'Investigating Officer Field Diary', value: 'Suspect vehicle registration DL-3C-AZ-9912 traced via toll gantry camera', restricted: true }
  ];

  // Administration Users
  readonly adminUsers = [
    { name: 'SI Rajat Mehra', email: 'r.mehra@delhipolice.gov.in', role: 'Police', agency: 'Noida Sec 58 PS' },
    { name: 'Dr. Anjali Iyer', email: 'a.iyer@cyberlab.gov.in', role: 'Forensics', agency: 'State Cyber Forensics Lab' },
    { name: 'Shalini Bhat', email: 's.bhat@prosecution.gov.in', role: 'Lawyer', agency: 'District Prosecution Branch' },
    { name: 'Hon. K. Mahadevan', email: 'k.mahadevan@ecourts.gov.in', role: 'Judge', agency: 'Sessions Court 04' },
    { name: 'Nikhil Rao', email: 'n.rao@ncrb.gov.in', role: 'Admin', agency: 'National Crime Records Bureau' }
  ];

  // Navigation Methods
  navigateTo(s: Screen | 'upload') {
    if (s === 'upload') {
      this.openUploadModal();
      return;
    }
    this.screen.set(s);
  }

  setRole(r: Role) {
    this.role.set(r);
    // If moving to a screen not allowed for this role, default to dashboard
    const allowed = this.navItems().map(item => item.target);
    const curr = this.screen();
    if (curr !== 'login' && !allowed.includes(curr) && curr !== 'case' && curr !== 'doc' && curr !== 'redact' && curr !== 'access') {
      this.screen.set('dash');
    }
  }

  // Upload Modal Workflow
  openUploadModal() {
    this.uploadOpen.set(true);
    this.uploadPhase.set('idle');
    this.uploadPct.set(0);
    this.liveHash.set('');
  }

  closeUploadModal() {
    this.uploadOpen.set(false);
    this.uploadPhase.set('idle');
    this.clearTimers();
  }

  startUploadProcess() {
    this.uploadPhase.set('progress');
    this.uploadPct.set(0);

    const intervalId = setInterval(() => {
      const p = Math.min(100, this.uploadPct() + 10);
      this.uploadPct.set(p);

      if (p >= 100) {
        clearInterval(intervalId);
        this.uploadPhase.set('hashing');
        this.scrambleLiveHash(H_UP, () => {
          setTimeout(() => {
            this.uploadPhase.set('done');
          }, 350);
        });
      }
    }, 80);

    this.activeTimers.push(intervalId);
  }

  // Document Integrity Verification
  verifyDocumentIntegrity() {
    this.verifyState.set('checking');
    setTimeout(() => {
      if (this.simulateTamper()) {
        this.verifyState.set('tampered');
      } else {
        this.verifyState.set('ok');
      }
    }, 1200);
  }

  // Chain Sweep Verification
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

  // Redaction Drawing Controls
  startDraft(x: number, y: number) {
    this.draft.set({ x, y, w: 0, h: 0 });
  }

  updateDraft(currentX: number, currentY: number) {
    const d = this.draft();
    if (!d) return;
    const originX = d.x;
    const originY = d.y;
    const x = Math.min(currentX, originX);
    const y = Math.min(currentY, originY);
    const w = Math.abs(currentX - originX);
    const h = Math.abs(currentY - originY);
    this.draft.set({ x, y, w, h });
  }

  endDraft() {
    const d = this.draft();
    if (d && d.w > 12 && d.h > 10) {
      const currentList = this.redactions();
      const id = currentList.length + 1;
      const dims = `${Math.round(d.w)}×${Math.round(d.h)} px @ ${Math.round(d.x)},${Math.round(d.y)}`;
      this.redactions.set([
        ...currentList,
        {
          id,
          x: d.x,
          y: d.y,
          w: d.w,
          h: d.h,
          dims,
          reason: 'Witness identity — §228A IPC / §72 BNS'
        }
      ]);
      this.redactSaved.set(false);
    }
    this.draft.set(null);
  }

  removeRedaction(id: number) {
    this.redactions.set(this.redactions().filter(r => r.id !== id));
  }

  saveRedactedCopy() {
    this.redactSaved.set(true);
  }

  toggleAuditRow(id: number) {
    this.expandedAuditId.set(this.expandedAuditId() === id ? null : id);
  }

  private scrambleLiveHash(target: string, onComplete: () => void) {
    let index = 0;
    this.liveHash.set('');

    const scrambler = setInterval(() => {
      index += 2;
      if (index >= 64) {
        clearInterval(scrambler);
        this.liveHash.set(target);
        onComplete();
        return;
      }
      let s = target.slice(0, index);
      for (let k = index; k < 64; k++) {
        s += HEX_CHARS[Math.floor(Math.random() * 16)];
      }
      this.liveHash.set(s);
    }, 28);

    this.activeTimers.push(scrambler);
  }

  private clearTimers() {
    this.activeTimers.forEach(t => clearInterval(t));
    this.activeTimers = [];
  }
}
