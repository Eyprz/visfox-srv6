# Architecture

VISFOX-SRv6 has two layers of its own, laid out after the Intent-Based Networking reference model
([RFC 9315](https://www.rfc-editor.org/rfc/rfc9315)): the **Application Layer**, where operators state
what they want, and the **Intent-Enabled Layer (IEL)**, which fulfills and assures it on the SRv6 network.
The contract between them is [`api/openapi.yaml`](../api/openapi.yaml) (REST and Server-Sent Events).

## Application Layer

A React application that stays declarative: it never computes or edits SRv6 SIDs and never talks to
the network. Its views:

- **Crosspoint matrix.** Each cell (IN x OUT) is an intent: which preset (service chain) the input carries
  to the output, and whether it is ON AIR or waits in STANDBY. A Take puts a STANDBY cell on air.
- **SFC designer.** Presets are logical chains of video functions (caption, color, transcode, crop) built
  on a React Flow canvas. Chain rules (VVF bundles, execution order, formats) come from the IEL.
- **Live parameters.** Function parameters change while on air, about every 100 ms.
- **Topology and real path.** The L7 topology, the path a cell takes, and per-hop diagnostics read from
  the IEL.

The UI only shows what the IEL decides: resource shortages, cells that cannot be taken and why, and
the STANDBY cells it may release to make room.

## Intent-Enabled Layer

A Go service. Every package matches a step of RFC 9315 and hands its result on southbound.

| RFC 9315 step | Package | In -> out |
| :--- | :--- | :--- |
| Intent ingestion | `internal/ingestion/nbi`, `internal/ingestion/usecase` | HTTP -> use cases; the intent lifecycle (OFF, STANDBY, ON AIR) in the SSoT |
| Intent translation | `internal/translation` | crosspoint intent -> SFC request (validated chain of SF instances) |
| Orchestration: plan | `internal/orchestration/plan` | SFC requests -> Service Function Paths (VVF nodes, ways through switchers) |
| Orchestration: render | `internal/orchestration/render` | SFP -> southbound plan (SR Policies, VVF control) |
| Orchestration: provision | `internal/orchestration/provision` | plan -> drivers, in order, stopping at the first failure |
| Southbound clients | `internal/sbi` | PolaPCE (gRPC), VVF control (router REST), BGP-LS (GoBGP gRPC), dry run |
| Intent assurance | `internal/assurance` | monitor (read back), compliance (compare with the SSoT), report (SSE, logs) |
| SSoT | `internal/ssot` | presets, intents, TED, the provisioned realization (SQLite) |

### The intent is the Single Source of Truth

Every intent is stored before anything is provisioned: presets (logical chains and parameters) and the
per-cell intents (which preset, on air or not, and the last known good config for rollback). What the
orchestrator derives from them (allocations, rendered policies) is stored too, but can always be rebuilt
from the intents and the TED. Assurance compares the network with the SSoT and repairs drift; it never
rewrites the intent.

Layout data of the designer (node coordinates) is kept apart from the logical chain.

### Topology (TED)

The TED is built in three steps: **data source -> YANG -> TED**.

1. The data source is a static file shaped like a BGP-LS advertisement, or the BGP-LS routes of a GoBGP
   collector (node names, router ids, SRv6 SIDs with behavior and structure, links).
2. It becomes a YANG instance (`ietf-network`, RFC 8345, augmented by `visfox-srv6-ted`) and is validated
   against the schema.
3. A **SID interpretation rule** from the network operator config derives the roles: headends, VVF
   nodes, switchers (one SID per input port), decaps. Node ids are derived from the SIDs, never assigned
   by hand. With structured SIDs (RFC 8986 `LOC:FUNCT:ARG`) a node's End SID is its node SID and the
   others are its services.

What the TED does not advertise lives in the network operator config: the IN / OUT bindings, the physical
wiring of the switcher inputs, and the limits of every resource.

### Video functions, switchers and resource pools

Video functions run on **VVFs** (Virtual Video Functions, SR-aware End.AN functions). A VVF's `behavior`
says what it does with streams:

- `process`: processes one stream (caption, color / transcode / crop);
- `select`: picks one of the streams on its input ports per output (a switcher);
- `replicate`: copies a stream to several destinations (a branch, RFC 9524).

Every finite resource is a **resource pool**: VVF nodes (one config each, shared by streams with the same
config up to a capacity), switcher outputs and inputs, and the streams a headend can send. A new limit is a
new pool kind, not a new API field. The planner allocates every stream again on every change; a shortage
becomes a `RESOURCE_EXHAUSTED` issue naming the pool and the nodes, and, when releasing STANDBY cells of
the same input would help, the cells to release.

The ways through the switchers are computed as a graph: one tree per OUT whose root switcher feeds the
OUT's processing VVFs. A switcher output serves one OUT; a switcher input fed by an IN can serve several
OUTs, one fed by another switcher carries a stream selected for one OUT only.

### Hot standby and the Take

How a STANDBY input waits is a network setting:

- **Hot standby:** STANDBY inputs are already sent to the OUT's switchers (a feed policy that stops
  after the switchers). A Take turns that policy into the whole path and switches. The picture changes
  at once.
- **Cold standby:** only the VVF nodes are reserved; the Take sets up the whole path.

A Take runs, in order: the VVF configs of the new chain (on nodes the picture on air does not use), the
SR Policy of the new stream, then the switcher selections, upstream first. The previous stream is turned
back into a feed afterwards. Failure at any step rolls the OUT back to its last known good config.

The planner keeps what flows:

- an ON AIR stream keeps its way;
- the input being taken stays on the switcher ports it is already fed on (no re-wiring at the Take);
- STANDBY ways stay too, and are packed again only when another request does not fit otherwise. A
  STANDBY's way does not show in the picture; feeds that move are provisioned in the same change.

### Switching test model

`iel/internal/ingestion/usecase/switching_test.go` checks the two requirements of video switching on
every `go test`, with named scenarios and seeded random sequences of operations:

- **The picture never breaks.** After every southbound instruction a network model computes what reaches
  each OUT. OUTs the operation is not about never change; the OUT of a Take changes once, from the old
  picture straight to the new one; with hot standby the input goes on air through the ports it was fed on.
- **Resources are used to the full.** An independent oracle searches every allocation that keeps the ON
  AIR ways and fits the limits. The IEL refuses an operation if and only if none exists.

It also checks that predictions (`take_issues`, validation) match the outcome, that the network holds
exactly the SR Policies of the SSoT, and that every takeable STANDBY input is fed.
`VISFOX_SWITCHING_SEEDS=150 go test ./internal/ingestion/usecase/ -run TestSwitching` runs a longer search.

Known limits reported by the model: a switcher keeps its last input when its OUT has nothing on air any
more, and a cold standby Take is not seamless.

### Assurance

- At startup the IEL reads the SR Policies back from PolaPCE, compares them with the SSoT, provisions the
  OUTs that drifted again and withdraws policies the SSoT does not have.
- A failed change rolls the affected OUTs back to their last known good config and reports it.
- Events (matrix updates, telemetry, rollbacks, switcher changes, logs) go to the WebUI over SSE.

### Southbound

| Instruction | Driver |
| :--- | :--- |
| SR Policy set / delete | `polapce`: PolaPCE (`CreateSRPolicy` / `DeleteSRPolicy`, PCEP to the headends) |
| VVF control: input selection of a switcher VVF (`select`) | `mgmt`: the router's REST API (`POST /config/stream_switch`), provisionally |
| VVF control: config of a processing VVF | `mgmt`: not defined yet |

VVF control has no proper M-plane yet: no component manages the SR functions, so the `mgmt` driver
provisionally uses the REST API of the routers the VVFs run on. It moves to that component (a VNF
manager or VIM) once one exists.

Every instruction kind can also go to the **dry-run** driver, which logs instead of sending: for health
checks, validation and demos without touching the network (`orchestrator.sr_policy` /
`orchestrator.vvf: dryrun`, or `VISFOX_DRY_RUN=true` for everything).

The IEL sends explicit Segment Lists of service SIDs (VVF functions, switcher input ports, decap); the IGP
routes between them. On a network whose TED is shared with PolaPCE over BGP-LS, Pola validates every SID
against its TED.
