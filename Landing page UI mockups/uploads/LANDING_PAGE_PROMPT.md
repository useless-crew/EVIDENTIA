# Landing Page Build Prompt
### Secure Digital DMS — Government Landing Page

Use the prompt below as-is when handing this off to a design tool, Claude Design, or a frontend
build agent. Section content is included so the output is populated with real, on-brand copy
rather than lorem ipsum.

---

## THE PROMPT

> Build a single-page marketing/landing page for **SuRakshit DMS** (working name — adjust if the
> project has a final name), a secure, cryptographically verifiable Digital Document Management
> System for India's criminal justice ecosystem (police, forensic labs, courts, and legal
> counsel).
>
> **This is a government-facing product.** Design it the way an official Government of India
> platform looks and feels — not a startup SaaS landing page. Reference the visual language of
> sites like india.gov.in, digitalindia.gov.in, and eCourts/NIC-built portals: restrained color,
> formal typography, visible trust/authority markers, and information density over marketing
> flourish. Avoid: gradients, large decorative illustrations, playful micro-copy, oversized hero
> imagery, rounded/pill buttons, casual language, stock photography of smiling people.
>
> **Visual direction:**
> - Base palette: navy `#132A49` (primary/header/CTA), light cool grey `#E6EAED` (page
>   background), white `#FFFFFF` (cards/sections), `#172536` (body text), `#68717C` (secondary
>   text), `#E2E6EA` (borders). This matches the product's existing internal design system —
>   the landing page must look like the front door to the same application, not a different brand.
> - A thin **tricolor accent strip** (saffron/white/green, 3–4px) may be used once, subtly — e.g.
>   as a top-of-page bar above the navbar — as a restrained nod to official Indian government
>   sites. Do not overuse tricolor; one instance only, thin, not decorative.
> - Typography: Inter or a similarly formal, highly legible sans-serif. No display/script fonts.
> - Buttons: rectangular-leaning (4–6px radius), navy fill/white text for primary actions, white
>   with navy border for secondary — no pill shapes, no drop shadows beyond near-invisible ones.
> - Iconography: outline style only, muted grey default (`#A7AFB7`), no filled icons, no emoji.
> - Layout: generous whitespace but information-dense — this is an institutional tool, not a
>   consumer app; visitors should feel this is serious, audited, and built for professionals.
> - Include standard Indian gov. trust cues where appropriate: an "Authorized / Government
>   Initiative" style badge or note, a footer disclaimer line, and language suggesting the
>   platform is built to align with statutory requirements (IT Act, Evidence Act) — do not
>   overclaim official government endorsement unless that's factually true for this project;
>   phrase it as "built in alignment with" rather than "endorsed by."
> - Fully responsive; mobile-first breakpoints; accessible (WCAG AA contrast minimum, no
>   information conveyed by color alone).
>
> **Page structure (in order):** Hero → Problem Statement → Solution/Value → How It Works →
> FAQ → Call to Action (+ standard footer). Build each section using the content specified below.
> Do not invent additional marketing sections (no testimonials, no pricing, no team/about-us
> carousel) — this is a focused institutional landing page, not a SaaS growth page.
>
> Use semantic HTML, accessible heading hierarchy (one H1 in the hero, H2 per section), and
> ensure all interactive elements (nav, buttons, FAQ accordion) are keyboard-navigable.

---

## SECTION-BY-SECTION CONTENT

Use this copy directly, or as the basis the build tool should follow closely — it's written to
match the project's actual functionality (from the master spec) rather than generic placeholder
claims.

### 1. Hero Section

**Layout note:** No large illustration. Optionally a subtle abstract graphic suggesting a
document + hash/chain motif (linear, not decorative), or no imagery at all — just strong
typography on the navy/grey palette, consistent with institutional sites.

- **Eyebrow/tag (optional, small text above headline):** `Secure Evidence & Case Management`
- **H1 (headline):** `A Single, Verifiable Record for Every Case File`
- **Subheadline:** `From FIR to final hearing — police, forensic labs, prosecutors, and courts
  work off one cryptographically verified document trail. Every file is fingerprinted on upload.
  Every action is logged. Nothing can be quietly altered.`
- **Primary CTA button:** `Request Access`
- **Secondary CTA button (text link or outline button):** `See How It Works`
- **Trust line beneath CTAs (small text):** `Built in alignment with the IT Act, 2000 (§65A/§65B)
  and the Bharatiya Sakshya Adhiniyam, 2023`

---

### 2. Problem Statement Section

**Layout note:** 3–4 short problem cards or a two-column text + stat-style layout. Sober tone —
this section should read like a policy brief, not a pain-point sales pitch.

- **H2:** `India's Investigative Records Are Fragmented — and Hard to Trust`
- **Intro paragraph:** `Case files move across police stations, forensic laboratories, legal
  counsel, and courts — but rarely through a shared, verifiable system. The result is lost time,
  disputed authenticity, and evidence that's difficult to defend in court.`

**Problem cards (3–4 short items):**

1. **Title:** `Disconnected Systems`
   **Body:** `Police stations, the CBI, forensic labs, and courts each maintain separate,
   non-communicating records — with no shared source of truth for a case.`

2. **Title:** `Unverifiable Documents`
   **Body:** `Physical files degrade or go missing. Digital copies circulate without
   confirmation of which version is authoritative or whether it has been altered.`

3. **Title:** `Weak Access Control`
   **Body:** `Sensitive information — witness identities, juvenile records — is often visible
   to more people than it should be, with no reliable record of who accessed what.`

4. **Title:** `No Audit Trail`
   **Body:** `When a document's history is questioned in court, most existing systems can't
   produce a tamper-evident record of who touched it, and when.`

---

### 3. Solution / Value Section

**Layout note:** Feature-grid style — 3 to 4 core value pillars, each with an icon (outline
style, muted grey), short title, and 1–2 line description. Keep claims grounded and specific,
not generic ("secure," "fast") without backing detail.

- **H2:** `One System, Built for Evidentiary Integrity`
- **Intro paragraph:** `Every document uploaded to the system is hashed, access-controlled, and
  logged from the moment it enters — so integrity isn't something you have to take on faith.`

**Value pillars:**

1. **Title:** `Cryptographic Integrity`
   **Body:** `Every document is fingerprinted with SHA-256 on upload. Any later change — even a
   single character — is immediately detectable.`

2. **Title:** `Role-Based Access`
   **Body:** `Judges, lawyers, police, and forensic experts each see only what their role permits
   — enforced at both the application and database level.`

3. **Title:** `Tamper-Evident Audit Trail`
   **Body:** `Every view, upload, and share is recorded in an append-only, chain-linked log.
   Attempting to alter history breaks the chain visibly.`

4. **Title:** `Built-In Legal Compliance`
   **Body:** `Section 65B certificates are generated automatically, aligning digital evidence
   with Indian evidentiary standards from the moment it's created.`

---

### 4. How It Works Section

**Layout note:** Numbered horizontal or vertical step sequence (4–6 steps), consistent with the
step-card pattern used elsewhere in the product. Keep each step short and procedural — this
section should read like a clear, credible process, not a feature list.

- **H2:** `From Upload to Verified Record`

**Steps:**

1. **Sign in by role** — `Officers, forensic experts, lawyers, and judges log in and see only the
   cases and documents relevant to their role.`
2. **Open or create a case** — `Case files bring together documents, involved parties, and a
   full timeline in one place.`
3. **Upload evidence** — `A document is uploaded — a forensic report, an FIR, a photograph — and
   the system immediately generates its cryptographic fingerprint.`
4. **Verify anytime** — `Any authorized user can re-check a document's integrity on demand and
   see instantly whether it matches its original fingerprint.`
5. **Redact when needed** — `Sensitive details can be redacted before sharing, creating a
   separate, independently verified copy — the original stays untouched.`
6. **Every action is logged** — `Uploads, views, shares, and redactions are written to a
   permanent, tamper-evident audit trail — provable, not just claimed.`

---

### 5. FAQ Section

**Layout note:** Standard accordion, consistent with institutional site conventions. Keep
answers factual and specific to this system, not marketing-toned.

**Q1: Who can use this system?**
`Access is limited to authorized personnel — investigating officers, forensic experts, legal
counsel, judges, and system administrators — each provisioned with a specific role that
determines what they can see and do.`

**Q2: How is document integrity guaranteed?**
`Every document is assigned a SHA-256 cryptographic hash the moment it's uploaded. Any
subsequent modification changes the hash, so tampering is mathematically detectable rather than
dependent on manual review.`

**Q3: Is this legally admissible as evidence?**
`The system is built to align with the Information Technology Act, 2000 (Sections 65A/65B) and
the Bharatiya Sakshya Adhiniyam, 2023, including automated generation of the certification
required for electronic records to be considered for admissibility.`

**Q4: What happens when a document is redacted?**
`Redaction never modifies the original file. It produces a new, separately hashed copy with the
sensitive regions removed, while the original remains securely preserved and unaltered.`

**Q5: Can access or activity be traced later?**
`Yes. Every view, upload, share, and modification is written to an append-only, cryptographically
chained audit log. The full chain can be independently re-verified at any time to confirm no
entry has been altered or removed.`

**Q6: How do I get access for my department or agency?**
`Access is provisioned by request. Use the "Request Access" button above, or contact your
department's assigned administrator to be added to the system.`

---

### 6. Call to Action Section

**Layout note:** Full-width navy (`#132A49`) band, centered content, white text — the one place
on the page where the primary color is used as a large background, signaling this is the closing/
action moment.

- **H2:** `Bring Your Case Records Onto One Verified System`
- **Body:** `Request access for your department, agency, or chambers and see how document
  integrity, access control, and audit logging work together in practice.`
- **Primary CTA button:** `Request Access`
- **Secondary link/button:** `Contact the Implementation Team`
- **Small print beneath:** `Authorized personnel only. All access to this system is logged.`

---

### 7. Footer (standard, not one of the five core sections but expected on a gov. site)

- Left: system name + one-line description
- Columns: `Resources` (How It Works, FAQ, Documentation), `Legal` (Privacy Policy, Terms of
  Access, Compliance & Standards), `Contact` (Implementation Team, Support)
- Bottom bar: `© [Year] [Project/Department Name]. All access to this system is logged.` +
  small note: `Built in alignment with the IT Act, 2000 and the Bharatiya Sakshya Adhiniyam, 2023.`

---

## Notes for Whoever Runs This Prompt

- Replace `SuRakshit DMS` with the final project name before use.
- If this is explicitly *not* an official government-endorsed product (e.g. a hackathon
  prototype), the compliance/trust language above should be phrased carefully — "built in
  alignment with" is accurate; avoid anything implying official government sign-off unless true.
- Keep this landing page visually and tonally consistent with the internal application's design
  system (Section 7 of `PROJECT_MASTER_SPEC.md`) so the transition from marketing page to login
  screen feels seamless, not like two different products.
