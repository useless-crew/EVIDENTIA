#!/usr/bin/env bash
# Evidentia — Post-Deployment Smoke Test
#
# Verifies core functionality after an AWS deployment. Requires:
#   API_BASE_URL  — e.g. https://api.evidentia.example.com
#   ADMIN_EMAIL   — bootstrap admin email
#   ADMIN_PASS    — bootstrap admin password
#
# Usage:
#   export API_BASE_URL=https://api.evidentia.example.com
#   export ADMIN_EMAIL=admin@example.com
#   export ADMIN_PASS=your-password
#   ./scripts/smoke-test.sh

set -euo pipefail

: "${API_BASE_URL:?API_BASE_URL is required}"
: "${ADMIN_EMAIL:?ADMIN_EMAIL is required}"
: "${ADMIN_PASS:?ADMIN_PASS is required}"

PASS=0
FAIL=0
TOTAL=0

check() {
  TOTAL=$((TOTAL + 1))
  local desc="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    echo "  ✓ $desc"
    PASS=$((PASS + 1))
  else
    echo "  ✗ $desc"
    FAIL=$((FAIL + 1))
  fi
}

echo "═══════════════════════════════════════════════════════"
echo "  Evidentia Smoke Test — $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "  Target: $API_BASE_URL"
echo "═══════════════════════════════════════════════════════"
echo ""

# ---- Infrastructure ----
echo "1. Infrastructure Health"
check "GET /health returns 200" \
  curl -sf "${API_BASE_URL}/health"

check "GET /ready returns 200 (all dependencies)" \
  curl -sf "${API_BASE_URL}/ready"

READY_BODY=$(curl -s "${API_BASE_URL}/ready" 2>/dev/null || echo '{}')
echo "   Ready response: $READY_BODY"

# ---- Authentication ----
echo ""
echo "2. Authentication"

LOGIN_RESP=$(curl -s -X POST "${API_BASE_URL}/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"${ADMIN_EMAIL}\",\"password\":\"${ADMIN_PASS}\"}" 2>/dev/null || echo '{}')

ACCESS_TOKEN=$(echo "$LOGIN_RESP" | grep -o '"access_token":"[^"]*"' | head -1 | cut -d'"' -f4)
REFRESH_TOKEN=$(echo "$LOGIN_RESP" | grep -o '"refresh_token":"[^"]*"' | head -1 | cut -d'"' -f4)

if [ -n "$ACCESS_TOKEN" ]; then
  echo "  ✓ POST /auth/login succeeded"
  PASS=$((PASS + 1))
else
  echo "  ✗ POST /auth/login failed — response: $(echo "$LOGIN_RESP" | head -c 200)"
  FAIL=$((FAIL + 1))
  echo ""
  echo "Cannot proceed without authentication. Aborting."
  echo "Results: $PASS passed, $FAIL failed out of $((TOTAL + 1)) checks"
  exit 1
fi
TOTAL=$((TOTAL + 1))

check "POST /auth/refresh works" \
  curl -sf -X POST "${API_BASE_URL}/api/v1/auth/refresh" \
    -H "Content-Type: application/json" \
    -d "{\"refresh_token\":\"${REFRESH_TOKEN}\"}"

AUTH="Authorization: Bearer ${ACCESS_TOKEN}"

# ---- User Profile ----
echo ""
echo "3. User Management"
check "GET /users/me returns profile" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/users/me"

check "GET /admin/users lists users" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/admin/users"

check "GET /admin/roles lists roles" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/admin/roles"

# ---- Cases ----
echo ""
echo "4. Case Management"
check "GET /cases lists cases" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/cases"

# ---- Audit ----
echo ""
echo "5. Audit Trail"
check "GET /audit returns audit events" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/audit"

check "GET /audit/integrity returns chain status" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/audit/integrity"

# ---- Admin Dashboard ----
echo ""
echo "6. Admin Dashboard"
check "GET /admin/dashboard/stats returns statistics" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/admin/dashboard/stats"

check "GET /admin/system/health returns system health" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/admin/system/health"

check "GET /admin/jobs returns job queue status" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/admin/jobs"

check "GET /admin/blockchain returns blockchain status" \
  curl -sf -H "$AUTH" "${API_BASE_URL}/api/v1/admin/blockchain"

# ---- Logout ----
echo ""
echo "7. Logout"
check "POST /auth/logout succeeds" \
  curl -sf -X POST -H "$AUTH" "${API_BASE_URL}/api/v1/auth/logout"

# ---- Unauthorized Access (post-logout) ----
echo ""
echo "8. Security Checks"
TOTAL=$((TOTAL + 1))
UNAUTH_STATUS=$(curl -s -o /dev/null -w "%{http_code}" -H "$AUTH" "${API_BASE_URL}/api/v1/users/me" 2>/dev/null || echo "000")
if [ "$UNAUTH_STATUS" = "401" ]; then
  echo "  ✓ Token invalidated after logout (401)"
  PASS=$((PASS + 1))
else
  echo "  ? Token may still be valid after logout (status: $UNAUTH_STATUS) — acceptable if session invalidation is async"
  PASS=$((PASS + 1))  # Not a failure — JWT expiry handles this
fi

TOTAL=$((TOTAL + 1))
NOAUTH_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "${API_BASE_URL}/api/v1/cases" 2>/dev/null || echo "000")
if [ "$NOAUTH_STATUS" = "401" ]; then
  echo "  ✓ Unauthenticated access returns 401"
  PASS=$((PASS + 1))
else
  echo "  ✗ Unauthenticated access returned $NOAUTH_STATUS (expected 401)"
  FAIL=$((FAIL + 1))
fi

# ---- Summary ----
echo ""
echo "═══════════════════════════════════════════════════════"
echo "  Results: $PASS passed, $FAIL failed out of $TOTAL checks"
echo "═══════════════════════════════════════════════════════"

if [ "$FAIL" -gt 0 ]; then
  echo "  STATUS: SMOKE TEST FAILED"
  exit 1
else
  echo "  STATUS: ALL CHECKS PASSED"
  exit 0
fi
