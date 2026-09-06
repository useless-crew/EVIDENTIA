package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets a small, fixed set of defense-in-depth response
// headers on every request (System 15 hardening pass). Evidentia is a pure
// JSON API with no server-rendered HTML and no cookie-based session, so
// this deliberately does not include a Content-Security-Policy — a CSP
// governs how a document renders scripts/styles/frames, which is
// meaningless for an `application/json` response and would be dead
// configuration.
//
//   - X-Content-Type-Options: nosniff — stop a browser from sniffing a
//     response's Content-Type and rendering/executing it as something
//     other than what the API declared (relevant even for JSON responses
//     if a browser is tricked into navigating to one directly).
//   - Referrer-Policy: no-referrer — case numbers, document IDs, and
//     other resource identifiers appear in request URLs; never leak them
//     to a third party via the Referer header on an outbound link/asset
//     load.
//   - X-Frame-Options: DENY — this API must never be embedded in a
//     frame; there is no legitimate product surface that needs it, and a
//     framed response is worth denying outright rather than negotiating
//     an allow-list.
//   - Cache-Control: no-store — every response from this API carries
//     evidence metadata, case data, audit information, or account
//     details for an authenticated user; none of it is safe for a
//     browser or intermediary cache/proxy to retain (master prompt §41).
//     Set unconditionally rather than per-route: there is no response in
//     this API for which caching would be correct, so there is nothing
//     an allow-list would need to exclude.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cache-Control", "no-store")
		c.Next()
	}
}
