# VISFOX-SRv6 🦊

**Video Intent-driven Service Function-chain Orchestrator & eXecutor on SRv6 infrastructure**

VISFOX-SRv6 lets video operators run live 8K video over an SRv6 network by saying *what* they want,
not *how* to forward it. An operator picks which input goes to which output and which video functions
(caption, color, transcode, crop) it passes through. VISFOX turns that intent into SRv6 Service
Function Chains (SFC), places them on the network's Virtual Video Functions (VVFs) and switchers,
installs the SR Policies through a PCE, and keeps checking that the network still matches the intent.

It follows the Intent-Based Networking reference model of [RFC 9315](https://www.rfc-editor.org/rfc/rfc9315):
an Application Layer where intents are expressed, and an Intent-Enabled Layer (IEL) that fulfills and
assures them.

## Highlights

- **Declarative operation.** A crosspoint matrix (IN x OUT) of intents, a drag-and-drop SFC designer and
  live function parameters. The WebUI never sees an SRv6 SID; it only reads them back for diagnostics.
- **IEL along RFC 9315.** Intent ingestion, translation, orchestration (plan / render / provision) and
  assurance, each in its own Go package, with the validated intents held as the Single Source of Truth
  (SSoT).
- **Real control plane.** SR Policies go to [PolaPCE](https://github.com/nttcom/pola) over gRPC and reach
  the headends over PCEP. The topology (TED) can be learned over BGP-LS from GoBGP and goes through a
  YANG model (RFC 8345) before the orchestrator sees it.
- **Seamless switching.** With hot standby every STANDBY input is already fed to the output's
  switchers, so a Take only switches: VVF configs first, then the path, then the switchers, upstream
  first (make-before-break). The on-air picture is never re-wired.
- **Resources as pools.** VVF nodes, switcher inputs / outputs and headend unicasts are uniform resource
  pools; a shortage is always the same kind of issue, and the IEL proposes which STANDBY cells to release.
- **Self-healing.** A failed change rolls back to the last known good config (LKG); at startup the IEL
  reads the SR Policies back from the PCE and repairs any drift.
- **Verified switching.** A test model replays every matrix operation against a network model and checks,
  after each southbound instruction, that no picture breaks, and compares every refusal with an
  exhaustive oracle of the possible allocations.

## Architecture

```mermaid
flowchart TB
    subgraph APP["Application Layer (React, TypeScript)"]
        UI["WebUI: crosspoint matrix, SFC designer, topology"]
    end
    subgraph IEL["Intent-Enabled Layer (Go)"]
        ING["Ingestion: NBI (REST / SSE), use cases"]
        TR["Translation: chain and format validation"]
        ORC["Orchestration: plan, render, provision"]
        AS["Assurance: monitor, compliance, report"]
        SSOT[("SSoT: intents, presets, TED, realization (SQLite)")]
    end
    subgraph CP["Control Plane"]
        PCE["PolaPCE"]
        BGP["GoBGP (BGP-LS)"]
    end
    subgraph DP["SRv6 domain"]
        HE["Headends (PCEP)"]
        VVF["VVFs (End.AN) and switchers"]
        DC["Decap"]
    end
    UI -- "REST / SSE (OpenAPI)" --> ING
    ING --> TR --> ORC
    ING <--> SSOT
    ORC -- "SR Policies (gRPC)" --> PCE
    PCE -- PCEP --> HE
    BGP -- "TED (gRPC)" --> SSOT
    BGP -- TED --> PCE
    AS -- "read back" --> PCE
    AS -- "drift, logs (SSE)" --> UI
    HE --> VVF --> DC
```

See [docs/architecture.md](docs/architecture.md) for the layers, the data flow and the switching rules.

## Quick start

Requirements: Docker with Compose.

```bash
# WebUI with the in-browser mock IEL
docker compose up --build -V
open http://localhost:15173

# WebUI with the Go IEL (SR Policies are logged, not sent)
VITE_API_MOCK=false docker compose up --build -V

# Local C-Plane lab: PolaPCE, GoBGP, a stand-in BGP-LS speaker and four stand-in headends
VITE_API_MOCK=false VISFOX_SR_POLICY=polapce docker compose --profile cplane up --build -V
docker compose exec visfox_headend_a wget -qO- localhost:8080/policies

# The lab network: the IEL learns its TED over BGP-LS, shared with PolaPCE
VITE_API_MOCK=false VISFOX_NETWORK=lab VISFOX_SR_POLICY=polapce \
  docker compose --profile cplane up --build -V
```

The networks you can run (`VISFOX_NETWORK`) and the lab are described in
[docs/networks.md](docs/networks.md).

## Repository layout

```text
visfox-srv6/
|-- api/openapi.yaml      # Contract between the Application Layer and the IEL (REST / SSE)
|-- application/          # Application Layer: React 18, TypeScript, Vite, Tailwind, React Flow
|   `-- src/mocks/        #   Stateful MSW mock IEL (runs the same rules in the browser)
|-- iel/                  # Intent-Enabled Layer: Go, Echo, SQLite
|   |-- cmd/visfox/       #   Entrypoint
|   |-- internal/         #   ingestion, translation, orchestration, sbi, assurance, ssot, domain
|   |-- config/           #   Network operator configs (SID rule, IN / OUT, wiring, limits)
|   |-- db/               #   TED sources, VVF catalog, presets, schema
|   `-- yang/             #   YANG model of the TED (RFC 8345 + visfox-srv6-ted)
|-- infra/
|   |-- nginx/            # Single-port gateway (WebUI and /api)
|   `-- cplane/           # Local C-Plane lab: PolaPCE, GoBGP, BGP-LS speaker, stand-in headends
`-- docker-compose.yaml
```

## Development

```bash
# API contract
npx @redocly/cli lint api/openapi.yaml

# Intent-Enabled Layer
cd iel && go test -race ./... && golangci-lint run

# Application Layer
cd application && npm run check && npm run lint && npm run test
```

## Status

VISFOX-SRv6 is under active development (v0.x). Switcher and VVF control are dry-run in this version
(the SR Policies are real); latency SLAs and telemetry-driven assurance are planned.

## License

[Apache License 2.0](LICENSE). The bundled fonts (Play, M PLUS 2) are under the SIL Open Font License,
and the IETF YANG modules under the license stated in each module.

## Contributors

See [CONTRIBUTORS.md](CONTRIBUTORS.md).
