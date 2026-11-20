# Security Policy

VISFOX-SRv6 is a research prototype of an SRv6 SFC orchestrator. It is meant for lab and testbed
networks, not for exposure to untrusted networks.

## Supported versions

Only the latest release (the newest `v0.x.y` tag) and `main` receive fixes.

## Reporting a vulnerability

Please do **not** open a public issue for a security problem. Report it privately through
GitHub: the repository's **Security** tab, then **Report a vulnerability** (private vulnerability
reporting). Include:

- the version (tag or commit, or `version` from `GET /api/v1/healthz`) and how you run it
  (mock IEL, Go IEL with dry run, C-Plane lab, a real network);
- what an attacker can do and the steps to reproduce it;
- logs or a proof of concept, with the addresses and credentials of your own network removed.

You can expect a first answer within two weeks. Once a fix is released, the advisory is published
and you are credited unless you ask not to be.

## Known limitations (by design, not vulnerabilities)

These are part of the current scope; reports about them are welcome as feature requests:

- The REST / SSE API of the Intent-Enabled Layer (IEL) and the WebUI have no authentication or
  TLS. Anyone who reaches the gateway port (`VISFOX_HTTP_PORT`, 15173 by default) can change intents.
- Southbound connections are unencrypted: gRPC to PolaPCE and GoBGP, and the routers' REST API
  used for VVF control.
- The local C-Plane lab (Compose profile `cplane`) uses fixed private addresses and stand-in
  devices for development only.

Run VISFOX-SRv6 on an isolated management network, or put it behind your own access control.
