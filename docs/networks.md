# Networks and the local C-Plane lab

The same IEL runs on different networks: everything a network imposes is in its TED source and its
network operator config, never in code. `VISFOX_NETWORK` selects one in Docker Compose; every network
keeps its own SSoT (database) and presets.

| `VISFOX_NETWORK` | TED source | Operator config | Network |
| :--- | :--- | :--- | :--- |
| (unset) | `iel/db/ted.yaml` | `iel/config/operator.yaml` | The testbed: 4 INs, 4 OUTs, two cascaded switchers (3 inputs each), 3 caption and 3 converter VVFs, one stream per headend, hot standby |
| `rich` | `iel/db/ted-rich.yaml` | `iel/config/operator-rich.yaml` | Plenty of resources: one switcher per OUT receiving every IN, 8 converters, 4 captions, no headend limit |
| `lab` | BGP-LS from GoBGP (`iel/db/ted-lab.yaml` is what the lab advertises) | `iel/config/operator-lab.yaml` | The testbed's composition on structured RFC 8986 SIDs with core routers; the TED is shared with PolaPCE, which validates the SIDs |

```bash
VISFOX_NETWORK=rich docker compose up -d visfox_iel
```

## Local C-Plane lab

The `cplane` Compose profile starts a control plane next to the IEL:

```text
visfox_iel --gRPC--> visfox_polad (PolaPCE, 172.31.77.2)
    |                  |  ^ TED (routes received from peers)
    | TED at startup   |  |
    | (lab, gRPC)      |  visfox_gobgpd (GoBGP, 172.31.77.3)
    +------------------|--^
                       |  ^ iBGP, BGP-LS
                       |  visfox_bgpls (stand-in BGP-LS speaker, 172.31.77.4)
                       v PCEP
            visfox_headend_a..d (stand-in headends, 172.31.77.11-14)
```

- **Stand-in headends** (`infra/cplane/fakepcc`) are PCEP clients built with Pola's PCEP library. They
  accept the SR Policies Pola initiates, report them as active and list them at `GET /policies`. They do
  not forward packets.
- **Stand-in BGP-LS speaker** (`infra/cplane/fakebgpls`) embeds GoBGP and advertises
  `iel/db/ted-lab.yaml` as BGP-LS routes (node, prefix, SRv6 SID and link NLRIs) to the lab's GoBGP over
  iBGP, as a router exporting its IGP would.

```bash
VITE_API_MOCK=false VISFOX_NETWORK=lab VISFOX_SR_POLICY=polapce \
  docker compose --profile cplane up --build -V

docker compose exec visfox_headend_a wget -qO- localhost:8080/policies
docker compose exec visfox_polad pola sr-policy list --host 172.31.77.2 -p 50052
docker compose exec visfox_polad pola ted --host 172.31.77.2 -p 50052
docker compose exec visfox_gobgpd gobgp neighbor
```

The networks share PolaPCE and the stand-in headends. At startup the IEL withdraws the VISFOX policies
its SSoT does not have, so switching networks replaces the policies of the previous one.

## Lab SID design

- One locator `fd00:0:NN::/48` per node (block 32 bits, node 16, function 16, no argument).
- `fd00:0:NN::` is the node's End SID (transit); `fd00:0:NN:F::` are its service SIDs: the VVF function,
  or one per switcher input port (`F` = `a`, `b`, ...).
- Every OUT has its own decap node, which advertises only its decap SID (End.DT6).
- Two core routers carry transit only and are not part of the IEL's TED.
