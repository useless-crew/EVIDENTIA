#!/usr/bin/env bash
#
# Evidentia — Synthetic Demo Data & End-to-End Flow (System 18)
#
# Exercises the REAL backend API (no direct DB writes, no bypassed auth/
# authz/RLS) to populate a working demo dataset and walk through the
# platform's primary evidence lifecycle: admin creates role accounts,
# police creates a case and uploads evidence, forensics verifies it,
# an ADMIN creates a redacted derivative, evidence is shared with a
# lawyer and a judge, a share is revoked, and a full cryptographic audit
# chain verification is run to completion via the real Asynq job + SSE
# progress path.
#
# ALL data created here is 100% FICTIONAL / SYNTHETIC — invented names,
# invented case facts, no real persons, no real case material. Every user
# gets a freshly-generated random password printed ONCE at the end of
# this run; nothing is hardcoded, and nothing is written back into any
# source file.
#
# Why an ADMIN performs redaction/certificate steps below rather than
# POLICE/FORENSICS: backend/db/seed/001_reference_data.sql's role→
# permission mapping does not grant document:redact to POLICE, or
# certificate:read/certificate:create to POLICE/FORENSICS — only ADMIN
# holds those (JUDGE separately holds certificate:read). This script
# reflects the REAL, currently-deployed authorization matrix; it never
# works around it. See the System 18 integration report for why this is
# flagged as a discrepancy against the demo narrative's role descriptions,
# not silently "fixed" here by granting new permissions.
#
# Usage:
#   BASE_URL=http://localhost:8080/api/v1 ./scripts/demo_seed.sh
#
# Requires: curl, jq, python3 (to synthesize one small PNG evidence file).
# Requires ADMIN bootstrap credentials in the environment (or a root
# .env with EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL/_PASSWORD already applied to
# the running backend) — see docker-compose.yml's own comment on
# EVIDENTIA_BOOTSTRAP_ADMIN_*.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080/api/v1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RUN_ID="$(openssl rand -hex 3)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

log()  { printf '\n\033[1;34m==>\033[0m %s\n' "$1"; }
ok()   { printf '    \033[1;32mOK\033[0m  %s\n' "$1"; }
info() { printf '    %s\n' "$1"; }
fail() { printf '    \033[1;31mFAIL\033[0m %s\n' "$1"; exit 1; }

# ---- admin bootstrap credentials ----
# Reads only these two specific keys out of the root .env (rather than
# `source`-ing the whole file) — .env may contain unquoted values with
# spaces (e.g. EVIDENTIA_BOOTSTRAP_ADMIN_NAME=System Administrator),
# which a plain `source` would try to execute as a command.
env_value() {
  local key="$1"
  [ -f "$REPO_ROOT/.env" ] || return 0
  grep -m1 "^${key}=" "$REPO_ROOT/.env" | cut -d= -f2-
}
if [ -z "${EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL:-}" ]; then
  EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL="$(env_value EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL)"
fi
if [ -z "${EVIDENTIA_BOOTSTRAP_ADMIN_PASSWORD:-}" ]; then
  EVIDENTIA_BOOTSTRAP_ADMIN_PASSWORD="$(env_value EVIDENTIA_BOOTSTRAP_ADMIN_PASSWORD)"
fi
ADMIN_EMAIL="${EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL:?Set EVIDENTIA_BOOTSTRAP_ADMIN_EMAIL (or provide repo-root .env)}"
ADMIN_PASSWORD="${EVIDENTIA_BOOTSTRAP_ADMIN_PASSWORD:?Set EVIDENTIA_BOOTSTRAP_ADMIN_PASSWORD (or provide repo-root .env)}"

# ---- tiny HTTP helpers (real HTTP calls against the real backend) ----
# req METHOD PATH TOKEN [JSON_BODY] -> prints response body, asserts 2xx.
req() {
  local method="$1" path="$2" token="${3:-}" body="${4:-}"
  local args=(-sS -o "$WORKDIR/resp.json" -w '%{http_code}' -X "$method" "$BASE_URL$path")
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  if [ -n "$body" ]; then
    args+=(-H "Content-Type: application/json" -d "$body")
  fi
  local status
  status="$(curl "${args[@]}")"
  if [[ "$status" -lt 200 || "$status" -ge 300 ]]; then
    echo "  Request failed: $method $path -> HTTP $status" >&2
    cat "$WORKDIR/resp.json" >&2
    exit 1
  fi
  cat "$WORKDIR/resp.json"
}

# req_expect_status METHOD PATH TOKEN EXPECTED_STATUS [BODY] -> asserts an
# exact status (used for the deliberate unauthorized-access checks).
req_expect_status() {
  local method="$1" path="$2" token="${3:-}" expected="$4" body="${5:-}"
  local args=(-sS -o "$WORKDIR/resp.json" -w '%{http_code}' -X "$method" "$BASE_URL$path")
  [ -n "$token" ] && args+=(-H "Authorization: Bearer $token")
  if [ -n "$body" ]; then
    args+=(-H "Content-Type: application/json" -d "$body")
  fi
  local status
  status="$(curl "${args[@]}")"
  if [ "$status" != "$expected" ]; then
    fail "expected HTTP $expected for $method $path, got $status: $(cat "$WORKDIR/resp.json")"
  fi
  ok "$method $path correctly returned $expected"
}

login() {
  local email="$1" password="$2"
  req POST /auth/login "" "$(jq -nc --arg e "$email" --arg p "$password" '{email:$e,password:$p}')" \
    | jq -r '.data.access_token'
}

random_password() { openssl rand -base64 18 | tr -d '=+/' | cut -c1-20; }

declare -A CREDS

# Takes the password as an argument (generated by the CALLER, in the main
# shell) rather than generating it internally — this function's own
# output is captured via `$(...)`, which runs in a subshell, so any
# assignment made only inside it (e.g. to the CREDS associative array)
# would silently vanish once the subshell exits.
create_user() {
  local admin_token="$1" email="$2" first="$3" last="$4" role="$5" password="$6"
  req POST /admin/users "$admin_token" \
    "$(jq -nc --arg e "$email" --arg p "$password" --arg f "$first" --arg l "$last" --arg r "$role" \
      '{email:$e,password:$p,first_name:$f,last_name:$l,role:$r,status:"active"}')" \
    | jq -r '.data.id'
}

# ============================================================
log "1. Admin bootstrap login"
ADMIN_TOKEN="$(login "$ADMIN_EMAIL" "$ADMIN_PASSWORD")"
[ -n "$ADMIN_TOKEN" ] && [ "$ADMIN_TOKEN" != "null" ] || fail "admin login did not return an access token"
ok "Authenticated as $ADMIN_EMAIL"

# ============================================================
log "2. Admin creates one synthetic account per role"
POLICE_EMAIL="officer.priya.sharma+$RUN_ID@evidentia.demo"
FORENSICS_EMAIL="dr.arjun.mehta+$RUN_ID@evidentia.demo"
LAWYER_EMAIL="adv.kavita.rao+$RUN_ID@evidentia.demo"
JUDGE_EMAIL="justice.vikram.nair+$RUN_ID@evidentia.demo"

CREDS["$POLICE_EMAIL"]="$(random_password)"
CREDS["$FORENSICS_EMAIL"]="$(random_password)"
CREDS["$LAWYER_EMAIL"]="$(random_password)"
CREDS["$JUDGE_EMAIL"]="$(random_password)"

POLICE_ID="$(create_user "$ADMIN_TOKEN" "$POLICE_EMAIL" "Priya" "Sharma" "POLICE" "${CREDS[$POLICE_EMAIL]}")"
ok "POLICE   $POLICE_EMAIL ($POLICE_ID)"
FORENSICS_ID="$(create_user "$ADMIN_TOKEN" "$FORENSICS_EMAIL" "Arjun" "Mehta" "FORENSICS" "${CREDS[$FORENSICS_EMAIL]}")"
ok "FORENSICS $FORENSICS_EMAIL ($FORENSICS_ID)"
LAWYER_ID="$(create_user "$ADMIN_TOKEN" "$LAWYER_EMAIL" "Kavita" "Rao" "LAWYER" "${CREDS[$LAWYER_EMAIL]}")"
ok "LAWYER   $LAWYER_EMAIL ($LAWYER_ID)"
JUDGE_ID="$(create_user "$ADMIN_TOKEN" "$JUDGE_EMAIL" "Vikram" "Nair" "JUDGE" "${CREDS[$JUDGE_EMAIL]}")"
ok "JUDGE    $JUDGE_EMAIL ($JUDGE_ID)"

# ============================================================
log "3. Police logs in and creates a case"
POLICE_TOKEN="$(login "$POLICE_EMAIL" "${CREDS[$POLICE_EMAIL]}")"
CASE_NUMBER="DEMO/$RUN_ID/0001"
CASE_JSON="$(req POST /cases "$POLICE_TOKEN" "$(jq -nc --arg n "$CASE_NUMBER" \
  '{case_number:$n,title:"State vs. Fictitious Devendra Kumar (SIH Demo)",
    description:"SYNTHETIC DEMO CASE — fictional facts, created by scripts/demo_seed.sh for System 18 demonstration purposes only.",
    status:"OPEN"}')")"
CASE_ID="$(echo "$CASE_JSON" | jq -r '.data.id')"
ok "Case $CASE_NUMBER created ($CASE_ID)"

# ============================================================
log "4. Police uploads a synthetic evidence file (PNG, so it is redactable)"
EVIDENCE_FILE="$WORKDIR/evidence.png"
python3 - "$EVIDENCE_FILE" <<'PY'
import struct, zlib, sys
path = sys.argv[1]
w, h = 400, 300
def chunk(tag, data):
    return struct.pack('>I', len(data)) + tag + data + struct.pack('>I', zlib.crc32(tag + data))
sig = b'\x89PNG\r\n\x1a\n'
ihdr = chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 2, 0, 0, 0))
row = b'\x00' + bytes([220, 220, 225]) * w
raw = row * h
idat = chunk(b'IDAT', zlib.compress(raw, 9))
iend = chunk(b'IEND', b'')
with open(path, 'wb') as f:
    f.write(sig + ihdr + idat + iend)
PY
UPLOAD_JSON="$(curl -sS -X POST "$BASE_URL/cases/$CASE_ID/documents" \
  -H "Authorization: Bearer $POLICE_TOKEN" \
  -F "document_type=PHOTO_EVIDENCE" \
  -F "description=SYNTHETIC demo evidence photo (System 18 seed data)" \
  -F "file=@$EVIDENCE_FILE;type=image/png")"
DOCUMENT_ID="$(echo "$UPLOAD_JSON" | jq -r '.data.id')"
DOCUMENT_HASH="$(echo "$UPLOAD_JSON" | jq -r '.data.sha256_hash')"
[ -n "$DOCUMENT_ID" ] && [ "$DOCUMENT_ID" != "null" ] || fail "upload did not return a document id: $UPLOAD_JSON"
ok "Document $DOCUMENT_ID uploaded, backend-computed sha256=$DOCUMENT_HASH"

# ============================================================
log "5. Police shares the evidence with Forensics (VERIFY) and Judge (VIEW)"
SHARE_FORENSICS_ID="$(req POST "/documents/$DOCUMENT_ID/share" "$POLICE_TOKEN" \
  "$(jq -nc --arg u "$FORENSICS_ID" '{user_id:$u,permission:"VERIFY",reason:"Demo: route to forensics for analysis"}')" \
  | jq -r '.data.share_id')"
ok "Shared with Forensics ($SHARE_FORENSICS_ID)"
SHARE_JUDGE_ID="$(req POST "/documents/$DOCUMENT_ID/share" "$POLICE_TOKEN" \
  "$(jq -nc --arg u "$JUDGE_ID" '{user_id:$u,permission:"VIEW",reason:"Demo: judicial review"}')" \
  | jq -r '.data.share_id')"
ok "Shared with Judge ($SHARE_JUDGE_ID)"

# ============================================================
log "6. Forensics logs in and verifies the evidence via the share"
FORENSICS_TOKEN="$(login "$FORENSICS_EMAIL" "${CREDS[$FORENSICS_EMAIL]}")"
VERIFY_JSON="$(req POST "/documents/$DOCUMENT_ID/verify" "$FORENSICS_TOKEN")"
VERIFY_STATUS="$(echo "$VERIFY_JSON" | jq -r '.data.status')"
[ "$VERIFY_STATUS" = "VERIFIED" ] || fail "expected VERIFIED, got $VERIFY_STATUS"
ok "Forensics verification result: $VERIFY_STATUS"

# ============================================================
log "7. Admin generates the compliance certificate for the original"
# (FORENSICS/POLICE hold no certificate:read/create permission in the
# current seed data — see this script's header comment.)
CERT_JSON="$(req GET "/documents/$DOCUMENT_ID/certificate" "$ADMIN_TOKEN")"
CERT_ID="$(echo "$CERT_JSON" | jq -r '.data.id')"
ok "Certificate $CERT_ID issued for the original document"

# ============================================================
log "8. Admin creates a redacted derivative (original document is left untouched)"
REDACT_JSON="$(req POST "/documents/$DOCUMENT_ID/redact" "$ADMIN_TOKEN" \
  '{"reason":"SYNTHETIC demo redaction — masking a fictional witness detail (System 18 seed data)","regions":[{"page":1,"x":20,"y":20,"width":120,"height":40}]}')"
DERIVATIVE_ID="$(echo "$REDACT_JSON" | jq -r '.data.document.id')"
DERIVATIVE_HASH="$(echo "$REDACT_JSON" | jq -r '.data.document.sha256_hash')"
ok "Redacted derivative $DERIVATIVE_ID created, independent sha256=$DERIVATIVE_HASH"
[ "$DERIVATIVE_HASH" != "$DOCUMENT_HASH" ] || fail "derivative hash must differ from the original"

req GET "/documents/$DOCUMENT_ID/download" "$ADMIN_TOKEN" >/dev/null
info "Confirmed the original document ($DOCUMENT_ID) is still independently downloadable/unchanged"

DERIVATIVE_CERT_JSON="$(req GET "/documents/$DERIVATIVE_ID/certificate" "$ADMIN_TOKEN")"
DERIVATIVE_CERT_ID="$(echo "$DERIVATIVE_CERT_JSON" | jq -r '.data.id')"
ok "Independent certificate $DERIVATIVE_CERT_ID issued for the derivative"

# ============================================================
log "9. Police shares the evidence with the Lawyer, then it is revoked"
SHARE_LAWYER_ID="$(req POST "/documents/$DOCUMENT_ID/share" "$POLICE_TOKEN" \
  "$(jq -nc --arg u "$LAWYER_ID" '{user_id:$u,permission:"VIEW",reason:"Demo: disclosure to defence counsel"}')" \
  | jq -r '.data.share_id')"
ok "Shared with Lawyer ($SHARE_LAWYER_ID)"

LAWYER_TOKEN="$(login "$LAWYER_EMAIL" "${CREDS[$LAWYER_EMAIL]}")"
req GET /shared/documents "$LAWYER_TOKEN" >/dev/null
ok "Lawyer can list shared documents"
req GET "/documents/$DOCUMENT_ID/download" "$LAWYER_TOKEN" >/dev/null
ok "Lawyer can download the shared document"

log "9a. Unauthorized-action check: Lawyer attempting to redact (VIEW share never grants redact)"
req_expect_status POST "/documents/$DOCUMENT_ID/redact" "$LAWYER_TOKEN" 403 \
  '{"reason":"attempted unauthorized redaction","regions":[{"page":1,"x":0,"y":0,"width":10,"height":10}]}'

log "9b. Police revokes the Lawyer's share"
req POST "/documents/$DOCUMENT_ID/shares/$SHARE_LAWYER_ID/revoke" "$POLICE_TOKEN" >/dev/null
ok "Share revoked"

log "9c. Confirming the Lawyer can no longer access the document through that share"
req_expect_status GET "/documents/$DOCUMENT_ID/download" "$LAWYER_TOKEN" 403

# ============================================================
log "10. Judge logs in, reviews the evidence, verification result, and certificate"
JUDGE_TOKEN="$(login "$JUDGE_EMAIL" "${CREDS[$JUDGE_EMAIL]}")"
req GET "/documents/$DOCUMENT_ID/download" "$JUDGE_TOKEN" >/dev/null
ok "Judge can access the shared evidence"
JUDGE_CERT_JSON="$(req GET "/documents/$DOCUMENT_ID/certificate" "$JUDGE_TOKEN")"
ok "Judge independently retrieved the compliance certificate (certificate_id=$(echo "$JUDGE_CERT_JSON" | jq -r '.data.id'))"

# ============================================================
log "11. Admin opens the audit dashboard and runs a full chain verification"
AUDIT_JSON="$(req GET "/audit?page=1&page_size=5" "$ADMIN_TOKEN")"
AUDIT_TOTAL="$(echo "$AUDIT_JSON" | jq -r '.data.meta.total')"
info "Audit ledger currently has $AUDIT_TOTAL entries"

START_JSON="$(req POST /audit/verify-chain "$ADMIN_TOKEN")"
VERIFICATION_ID="$(echo "$START_JSON" | jq -r '.data.verification_id')"
ok "Verification $VERIFICATION_ID queued (Asynq job accepted, 202)"

for i in $(seq 1 60); do
  STATUS_JSON="$(req GET "/audit/verify-chain/$VERIFICATION_ID" "$ADMIN_TOKEN")"
  STATUS="$(echo "$STATUS_JSON" | jq -r '.data.status')"
  CHECKED="$(echo "$STATUS_JSON" | jq -r '.data.entries_checked')"
  info "  poll $i: $STATUS ($CHECKED entries checked)"
  case "$STATUS" in
    VERIFIED|INTEGRITY_FAILURE|FAILED) break ;;
  esac
  sleep 1
done
[ "$STATUS" = "VERIFIED" ] || fail "expected chain verification to reach VERIFIED, got $STATUS"
ok "Cryptographic audit chain verification: $STATUS"

# ============================================================
log "12. Unauthorized-access check: Forensics attempting ADMIN-only audit verification"
req_expect_status POST /audit/verify-chain "$FORENSICS_TOKEN" 403

# ============================================================
log "Demo data summary (SYNTHETIC — safe to share on-screen during a demo)"
cat <<EOF

  Case:        $CASE_NUMBER ($CASE_ID)
  Original:    $DOCUMENT_ID  sha256=$DOCUMENT_HASH
  Derivative:  $DERIVATIVE_ID  sha256=$DERIVATIVE_HASH
  Certificate: $CERT_ID (original), $DERIVATIVE_CERT_ID (derivative)
  Audit chain: $STATUS

  Accounts (all fictional — generated fresh this run, shown once):
    ADMIN      $ADMIN_EMAIL          (your existing bootstrap admin password)
    POLICE     $POLICE_EMAIL   ${CREDS[$POLICE_EMAIL]}
    FORENSICS  $FORENSICS_EMAIL   ${CREDS[$FORENSICS_EMAIL]}
    LAWYER     $LAWYER_EMAIL      ${CREDS[$LAWYER_EMAIL]}   (its share was revoked above)
    JUDGE      $JUDGE_EMAIL   ${CREDS[$JUDGE_EMAIL]}

EOF
log "Demo seed complete — every step above was a real call against $BASE_URL"
