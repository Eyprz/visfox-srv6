-- VISFOX-SRv6 IEL SQLite schema.
-- The TED tables are rebuilt from the TED source (Data Source -> YANG -> SID rule) at every start.

-- TED: nodes with the role derived by the SID interpretation rule. ids are derived from the SIDs.
CREATE TABLE IF NOT EXISTS ted_nodes (
    id        TEXT PRIMARY KEY,
    role      TEXT NOT NULL,          -- headend, switcher, vvf, decap
    label     TEXT NOT NULL,
    hostname  TEXT NOT NULL UNIQUE,   -- advertised node name (BGP-LS)
    router_id TEXT,
    vvf_id    TEXT                    -- VVF definition id (role vvf)
);

-- TED: SIDs (without the argument). Switcher inputs carry the port letter and the `next` index.
CREATE TABLE IF NOT EXISTS ted_sids (
    sid     TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES ted_nodes(id) ON DELETE CASCADE,
    port    TEXT,                     -- switcher input port (a, b, ...)
    next    INTEGER                   -- switcher select index (POST /config/stream_switch)
);

-- IN (sources) and OUT (destinations) bound to their headend / decap (network operator config).
CREATE TABLE IF NOT EXISTS sources (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    video_format TEXT NOT NULL,
    node_id      TEXT NOT NULL REFERENCES ted_nodes(id)
);

CREATE TABLE IF NOT EXISTS destinations (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    video_format TEXT NOT NULL,
    node_id      TEXT NOT NULL REFERENCES ted_nodes(id)
);

-- Presets (SSoT, logical chains): the function sequence and per-preset params.
CREATE TABLE IF NOT EXISTS presets (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT,
    chain_json  TEXT NOT NULL,       -- []FunctionInstance
    sla_json    TEXT,                -- default SLA (kept for v2; v1 ignores SLAs)
    created_at  TEXT NOT NULL,       -- RFC 3339
    updated_at  TEXT NOT NULL
);

-- SVoT: React Flow positions of a preset in the designer.
CREATE TABLE IF NOT EXISTS preset_layouts (
    preset_id   TEXT PRIMARY KEY REFERENCES presets(id) ON DELETE CASCADE,
    layout_json TEXT NOT NULL        -- PresetLayout
);

-- Active intents: the state of every matrix cell (IN x OUT). No foreign keys to the TED tables,
-- which are rebuilt at every start; cells of vanished IN / OUT are dropped at startup.
CREATE TABLE IF NOT EXISTS matrix_cells (
    cell_id            TEXT PRIMARY KEY, -- IN1-OUT1
    source_id          TEXT NOT NULL,
    destination_id     TEXT NOT NULL,
    execution_state    TEXT NOT NULL,    -- active, standby, off, disconnected
    health_state       TEXT,             -- active / standby only
    preset_id          TEXT REFERENCES presets(id),
    last_rollback_json TEXT,             -- RollbackInfo
    lkg_json           TEXT,             -- CellConfigSnapshot: last known good (preset, live chain)
    updated_at         TEXT NOT NULL
);

-- What Intent Orchestration derived from the intents and provisioned.
-- It is SSoT too: it is reloaded at startup and compared with what the network reports.

-- The Service Function Path selected for every OUT with an applied intent.
CREATE TABLE IF NOT EXISTS stream_paths (
    destination_id TEXT PRIMARY KEY,
    sfp_json       TEXT NOT NULL,        -- domain.SFP: switcher hops and one VVF node per SF instance
    updated_at     TEXT NOT NULL
);

-- The config every VVF node runs (provisioned function.set_params).
CREATE TABLE IF NOT EXISTS vvf_node_configs (
    node_id    TEXT PRIMARY KEY,
    config     TEXT NOT NULL,            -- domain.SFInstance.Config
    updated_at TEXT NOT NULL
);

-- The SR Policies provisioned on the headends (sr_policy.set minus sr_policy.delete). A headend
-- tells policies apart by color and endpoint (RFC 9256).
CREATE TABLE IF NOT EXISTS sr_policies (
    headend      TEXT NOT NULL,          -- TED node id
    color        INTEGER NOT NULL,
    endpoint     TEXT NOT NULL,          -- TED node id
    name         TEXT NOT NULL,
    segment_list TEXT NOT NULL,          -- JSON array of SIDs with their arguments
    updated_at   TEXT NOT NULL,
    PRIMARY KEY (headend, color, endpoint)
);

-- Settings changed through the WebUI (DynamicConfig) and other key / value state.
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,              -- dynamic_config, presets_seeded
    value TEXT NOT NULL
);
