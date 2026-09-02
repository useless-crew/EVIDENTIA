import { Injectable } from '@angular/core';
import { FaqItem, ProblemCard, FeaturePillar, ProcessStep } from '../models/landing.model';

@Injectable({
  providedIn: 'root'
})
export class LandingDataService {

  getProblemCards(): ProblemCard[] {
    return [
      {
        title: 'Disconnected Systems',
        body: 'Police stations, the CBI, forensic labs, and courts each maintain separate, non-communicating records — with no shared source of truth for a case.',
        iconSvgPath: 'M6 12a3 3 0 100-6 3 3 0 000 6zM18 12a3 3 0 100-6 3 3 0 000 6zM9 12h2M13 12h2'
      },
      {
        title: 'Unverifiable Documents',
        body: 'Physical files degrade or go missing. Digital copies circulate without confirmation of which version is authoritative or whether it has been altered.',
        iconSvgPath: 'M5 3h14a1.5 1.5 0 011.5 1.5v15A1.5 1.5 0 0119 21H5a1.5 1.5 0 01-1.5-1.5v-15A1.5 1.5 0 015 3zM10.5 10a1.5 1.5 0 112.3 1.3c-.5.35-.8.75-.8 1.3M12 15.2h.01'
      },
      {
        title: 'Weak Access Control',
        body: 'Sensitive information — witness identities, juvenile records — is often visible to more people than it should be, with no reliable record of who accessed what.',
        iconSvgPath: 'M6 11h12v9H6zM8.5 11V8a3.5 3.5 0 016.2-2.2'
      },
      {
        title: 'No Audit Trail',
        body: 'When a document\'s history is questioned in court, most existing systems can\'t produce a tamper-evident record of who touched it, and when.',
        iconSvgPath: 'M5 6h14M5 12h14M5 18h8M16 16l3.5 3.5M19.5 16L16 19.5'
      }
    ];
  }

  getFeaturePillars(): FeaturePillar[] {
    return [
      {
        title: 'Cryptographic Integrity',
        body: 'Every document is fingerprinted with SHA-256 on upload. Any later change — even a single character — is immediately detectable.',
        iconSvgPath: 'M12 3l7 3v5c0 4.5-3 7.5-7 9-4-1.5-7-4.5-7-9V6l7-3zM9 12l2 2 4-4'
      },
      {
        title: 'Role-Based Access',
        body: 'Judges, lawyers, police, and forensic experts each see only what their role permits — enforced at both the application and database level.',
        iconSvgPath: 'M12 11.2a3.2 3.2 0 100-6.4 3.2 3.2 0 000 6.4zM5.5 20c0-3.6 2.9-6 6.5-6s6.5 2.4 6.5 6'
      },
      {
        title: 'Tamper-Evident Audit Trail',
        body: 'Every view, upload, and share is recorded in an append-only, chain-linked log. Attempting to alter history breaks the chain visibly.',
        iconSvgPath: 'M7 9.4a2.4 2.4 0 100-4.8 2.4 2.4 0 000 4.8zM17 9.4a2.4 2.4 0 100-4.8 2.4 2.4 0 000 4.8zM12 19.4a2.4 2.4 0 100-4.8 2.4 2.4 0 000 4.8zM8.7 8.5l1.9 6.5M15.3 8.5l-1.9 6.5'
      },
      {
        title: 'Built-In Legal Compliance',
        body: 'Section 65B certificates are generated automatically, aligning digital evidence with Indian evidentiary standards from the moment it\'s created.',
        iconSvgPath: 'M5 3h14v18H5zM8.5 8.5h7M8.5 12h5M8 16.5l1.8 1.8 3.7-3.8'
      }
    ];
  }

  getProcessSteps(): ProcessStep[] {
    return [
      {
        stepNumber: 1,
        title: 'Sign in by role',
        description: 'Officers, forensic experts, lawyers, and judges log in and see only the cases and documents relevant to their role.'
      },
      {
        stepNumber: 2,
        title: 'Open or create a case',
        description: 'Case files bring together documents, involved parties, and a full timeline in one place.'
      },
      {
        stepNumber: 3,
        title: 'Upload evidence',
        description: 'A document is uploaded — a forensic report, an FIR, a photograph — and the system immediately generates its cryptographic fingerprint.'
      },
      {
        stepNumber: 4,
        title: 'Verify anytime',
        description: 'Any authorized user can re-check a document\'s integrity on demand and see instantly whether it matches its original fingerprint.'
      },
      {
        stepNumber: 5,
        title: 'Redact when needed',
        description: 'Sensitive details can be redacted before sharing, creating a separate, independently verified copy — the original stays untouched.'
      },
      {
        stepNumber: 6,
        title: 'Every action is logged',
        description: 'Uploads, views, shares, and redactions are written to a permanent, tamper-evident audit trail — provable, not just claimed.'
      }
    ];
  }

  getFaqList(): FaqItem[] {
    return [
      {
        q: 'Who can use this system?',
        a: 'Access is limited to authorized personnel — investigating officers, forensic experts, legal counsel, judges, and system administrators — each provisioned with a specific role that determines what they can see and do.',
        open: true
      },
      {
        q: 'How is document integrity guaranteed?',
        a: 'Every document is assigned a SHA-256 cryptographic hash the moment it\'s uploaded. Any subsequent modification changes the hash, so tampering is mathematically detectable rather than dependent on manual review.',
        open: false
      },
      {
        q: 'Is this legally admissible as evidence?',
        a: 'The system is built to align with the Information Technology Act, 2000 (Sections 65A/65B) and the Bharatiya Sakshya Adhiniyam, 2023, including automated generation of the certification required for electronic records to be considered for admissibility.',
        open: false
      },
      {
        q: 'What happens when a document is redacted?',
        a: 'Redaction never modifies the original file. It produces a new, separately hashed copy with the sensitive regions removed, while the original remains securely preserved and unaltered.',
        open: false
      },
      {
        q: 'Can access or activity be traced later?',
        a: 'Yes. Every view, upload, share, and modification is written to an append-only, cryptographically chained audit log. The full chain can be independently re-verified at any time to confirm no entry has been altered or removed.',
        open: false
      },
      {
        q: 'How do I get access for my department or agency?',
        a: 'Access is provisioned by request. Use the "Request Access" button above, or contact your department\'s assigned administrator to be added to the system.',
        open: false
      }
    ];
  }
}
