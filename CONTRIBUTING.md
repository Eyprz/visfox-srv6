# Contributing to VISFOX-SRv6

Thanks for your interest in VISFOX-SRv6. Bug reports, ideas and pull requests are welcome.

## How this repository is maintained

This repository is published from the maintainer's development repository at every release. An
accepted pull request is applied there, with your authorship kept, and appears here with the
next release. Because of that, a pull request may be closed as merged without a GitHub merge.

## Before you start

- **Bugs and features:** open an issue with the bug report or feature request form first, so we can
  agree on the approach before you spend time on it. Small fixes (typos, obvious bugs) can go
  straight to a pull request.
- **Security problems:** report them privately, as described in [SECURITY.md](SECURITY.md).

## Development setup

You need Docker with Compose, Go 1.27 or later, and Node.js 22. The [Quick start](README.md#quick-start)
starts the WebUI, the Intent-Enabled Layer (IEL) and, with the Compose profile `cplane`, a local
control plane (PolaPCE, GoBGP, a stand-in BGP-LS speaker and stand-in headends).
[docs/networks.md](docs/networks.md) describes the networks and the lab.

## Architecture rules

[docs/architecture.md](docs/architecture.md) describes the design. Changes must keep these rules:

- **The WebUI is declarative.** It says what the operator wants (IN, OUT, presets of functions). It
  never computes or edits SRv6 SIDs or Segment Lists, never calls routers or other network devices,
  and never hard-codes chain rules or resource limits. It reads them from the IEL, and the IEL's
  validation is the authority.
- **The API contract comes first.** Change [`api/openapi.yaml`](api/openapi.yaml), regenerate the
  types, then change the IEL and the WebUI. Never edit `api/generated/` by hand.
- **The IEL follows RFC 9315.** Packages follow its steps: ingestion, translation, orchestration
  (plan, render, provision) and assurance, on top of the SSoT. Only `internal/ingestion/nbi` knows
  HTTP, and only `internal/sbi` knows the southbound protocols. Name things after RFC 9315 and SRv6
  SFC terms, without `Service` / `Manager` / `Helper` suffixes.
- **The intent is the Single Source of Truth.** Every intent is stored before anything is
  provisioned, and what is derived from it can be rebuilt from the intents and the TED.
- **Constraints live in config.** How a network behaves (SID interpretation, wiring, resource
  limits) belongs in the network operator config and the TED source, not in code. A new limit is a
  new resource pool kind, not a new API field.
- **Southbound clients have fakes.** Every client in `internal/sbi` is tested against a fake of
  PolaPCE, GoBGP or the router API.

## Checks

Run these before you open a pull request:

```bash
# API contract
npx @redocly/cli lint api/openapi.yaml
cd application && npm run codegen   # after changing api/openapi.yaml
cd iel && go generate ./...         # after changing api/openapi.yaml

# Intent-Enabled Layer
cd iel
go fmt ./...
go test -race ./...
golangci-lint run

# Application Layer
cd application
npm run check
npm run lint
npm run test
```

`go test` includes the switching test model (`iel/internal/ingestion/usecase/switching_test.go`).
After changing allocation, rendering or the matrix use cases, also run a longer search:

```bash
cd iel && VISFOX_SWITCHING_SEEDS=150 go test ./internal/ingestion/usecase/ -run TestSwitching
```

## Commits and pull requests

Branch from `main` with a prefixed name (`feat/<name>`, `fix/<name>`, `docs/<name>`, `chore/<name>`).
Write commits in [Conventional Commits](https://www.conventionalcommits.org/) form with a bulleted
body:

```text
<type>(<scope>): <message>

- What changes
- Why
```

- **Types:** `feat`, `fix`, `docs`, `chore`, `refactor`, `test`.
- **Scopes:**
  - Application Layer: `app`.
  - IEL: `iel` for changes across the IEL. Otherwise the `iel/internal/` package: `ingestion`,
    `translation`, `orchestration`, `sbi`, `ssot`, `assurance`.
  - Others: `api` (the contract), `spec` (documentation), `infra` (Compose, nginx, the C-Plane lab).
- **One concern per commit.** Keep code and documentation changes in separate commits.

Fill in the pull request template, and say how you tested the change: mock IEL, Go IEL dry run,
C-Plane lab, or a real network.

Text diagrams and directory trees in `text` code blocks use ASCII characters only.

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE), as stated in section 5 of the license.
