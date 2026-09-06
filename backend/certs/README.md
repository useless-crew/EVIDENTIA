# TLS Certificates

This directory holds the TLS certificate/private key the **reverse
proxy** (`ops/reverse-proxy/`, System 17) mounts for HTTPS termination —
not the Go backend itself, which never terminates TLS directly (see
`docs/DEPLOYMENT.md`'s "TLS / HTTPS" section for the full architecture).

Expected files (both gitignored — see the root `.gitignore`'s
`*.pem`/`*.key`/`*.crt` rules; only this README is tracked):

- `fullchain.pem` — the certificate (plus any intermediate chain)
- `privkey.pem` — the private key

## Local / demo use

```bash
./ops/reverse-proxy/generate-dev-certs.sh
```

Generates a self-signed certificate here. Browsers will show a
certificate-trust warning — expected and correct for a self-signed
certificate, not a bug.

## Real production use

Replace both files with a certificate from your CA or ACME provider
(e.g. Let's Encrypt via `certbot`) for your actual domain. Never commit
real certificate/key material to this repository.

**Permissions matter**: the reverse proxy runs nginx as a non-root user
(`nginxinc/nginx-unprivileged`) and reads these files via a read-only
bind mount, so `privkey.pem` must be readable by whatever UID that
container actually runs as — a real CA/ACME tool's default `600` (owner-
only, typically root) will fail nginx startup with a silent-looking
"Permission denied" until you loosen it (the dev script above uses `644`
for exactly this reason). Prefer the least-permissive mode that the
container can still read over blindly using `644`/`666` for a real key.

## Never commit

- `*.pem`, `*.key`, `*.crt` — already gitignored.
- Do not paste certificate or key contents into commit messages, issues,
  or documentation.
