import type {
  ChainConstraints,
  DestinationNode,
  DynamicConfig,
  ExecutionState,
  FunctionCatalogItem,
  FunctionType,
  HealthState,
  Preset,
  PresetLayout,
  SourceNode,
  SwitcherNode,
} from '../lib/types'

// Mock seed data for the in-browser mock IEL. In the Go IEL these come from static seed files (TED via YANG).
// Text produced by the IEL is English.

/**
 * A VVF node in the TED: one End.AN (SID) on a router. L3 data is for diagnostics only.
 * `max_streams` (the vvf_node pool capacity): streams with the same config the node can process at once (bandwidth /
 * performance); omitted means no limit.
 */
export type VvfNode = {
  /** Display name. Pinned VVF nodes are told apart by it (e.g. the material a caption node burns in). */
  id: string
  vvf_id: string
  label: string
  sid: string
  router_id: string
  max_streams?: number
}

/**
 * VVF nodes in the TED (streams = OUTs with an ACTIVE or STANDBY cell): a VVF node holds one config
 * (resource pool sharing same_config). A color / transcode / crop node takes one stream, so the three of them allow three
 * streams; a caption node takes up to three streams when they insert the same caption. The two
 * switchers (one stream each) are the tighter limit in this mock.
 * Node counts do not limit preset design; they are checked when a preset is assigned.
 */
export const vvfNodes: VvfNode[] = [
  // Caption nodes are pinned: each burns in its own material, so presets name the node.
  ...['Logo', 'Score', 'Sponsor'].map((material, i) => ({
    id: `caption-${i + 1}`,
    vvf_id: 'vvf-caption',
    label: `Caption: ${material}`,
    sid: `fc00:1::${101 + i}`,
    router_id: `rtr-1${i + 1}`,
    // NIC bandwidth: about three 8K streams with the same caption (no current use case shares one).
    max_streams: 3,
  })),
  ...[1, 2, 3].map((n) => ({
    id: `color-transcode-crop-${n}`,
    vvf_id: 'vvf-color-transcode-crop',
    label: `Color / Transcode / Crop #${n}`,
    sid: `fc00:1::${110 + n}`,
    router_id: `rtr-2${n}`,
    max_streams: 1,
  })),
]

/**
 * VVF definitions. Pinned VVFs list their nodes (presets pick one per function, `vvf_node_id`);
 * dynamic VVFs have interchangeable nodes that the IEL assigns whenever streams are (re)allocated.
 */
const vvfs: ChainConstraints['vvfs'] = [
  {
    id: 'vvf-caption',
    name: 'Caption',
    behavior: 'process',
    function_types: ['caption'],
    require_all: true,
    contiguous: true,
    ordered: false,
    allocation: 'pinned',
    nodes: vvfNodes
      .filter((n) => n.vvf_id === 'vvf-caption')
      .map((n) => ({ node_id: n.id, label: n.label })),
  },
  {
    // function_types is the execution order the End.AN requires; the IEL reorders chains into it.
    id: 'vvf-color-transcode-crop',
    name: 'Color / Transcode / Crop',
    behavior: 'process',
    function_types: ['transcode', 'crop', 'color'],
    require_all: true,
    contiguous: true,
    ordered: false,
    allocation: 'dynamic',
    nodes: [],
  },
  // VVFs that control the stream flow: no functions, the IEL places them on the streams' ways.
  {
    id: 'vvf-switcher',
    name: 'Switcher',
    behavior: 'select',
    function_types: [],
    require_all: false,
    contiguous: false,
    ordered: false,
    allocation: 'dynamic',
    nodes: [],
  },
  {
    id: 'vvf-replicator',
    name: 'Replicator',
    behavior: 'replicate',
    function_types: [],
    require_all: false,
    contiguous: false,
    ordered: false,
    allocation: 'dynamic',
    nodes: [],
  },
]

/**
 * Switchers in the TED (role vvf, own structure). Every stream must pass a switcher (domain policy
 * switcher_required). IN 1-4 (A-D) are wired to both SW1 and SW2; a switcher sends out one stream
 * (max_outputs), so at most two streams (OUTs) run at once. A switcher NIC receives at most three 8K
 * streams at a time (max_inputs, 100G). SW2's output goes either to the processing VVFs or into SW1
 * (input e). Inputs are lettered with encap (source) inputs first, upstream switchers after.
 */
/** A switcher in the seed: the API structure plus its TED limits (exposed as resource pools). */
export type SwitcherSeed = SwitcherNode & { max_inputs: number; max_outputs: number }

export const switchers: SwitcherSeed[] = [
  {
    id: 'sw-1',
    label: 'SW1',
    router_id: 'rtr-06',
    sid: 'fc00:1::120',
    max_inputs: 3,
    max_outputs: 1,
    inputs: [
      { port: 'a', from: 'in-1' },
      { port: 'b', from: 'in-2' },
      { port: 'c', from: 'in-3' },
      { port: 'd', from: 'in-4' },
      { port: 'e', from: 'sw-2' },
    ],
    destination_ids: ['out-1', 'out-2', 'out-3', 'out-4'],
    active_input: null,
  },
  {
    id: 'sw-2',
    label: 'SW2',
    router_id: 'rtr-07',
    sid: 'fc00:1::121',
    max_inputs: 3,
    max_outputs: 1,
    inputs: [
      { port: 'a', from: 'in-1' },
      { port: 'b', from: 'in-2' },
      { port: 'c', from: 'in-3' },
      { port: 'd', from: 'in-4' },
    ],
    // To the processing VVFs (any OUT) or into SW1.
    destination_ids: ['out-1', 'out-2', 'out-3', 'out-4'],
    active_input: null,
  },
]

/**
 * Domain policy: every IN is 8K DG and every OUT expects 8K FULL (single SRv6 SFC domain).
 * No function count limits (max_functions / max_instances_per_type omitted): presets are free
 * designs, and the TED resources (VVF nodes) are checked when a preset is assigned to a cell.
 */
export const constraints: ChainConstraints = {
  topology: 'linear',
  allow_loops: false,
  min_functions: 0,
  vvfs,
  domain: {
    source_format: { video_format: '8k_dg' },
    destination_format: { video_format: '8k_full' },
    switcher_required: true,
  },
}

const domainIn = constraints.domain.source_format
const domainOut = constraints.domain.destination_format

// Display names are TED node names; the UI derives the short IN n / OUT n label from the id.
const sourceNames = ['KAIT-8K-01', 'KAIT-8K-02', 'GINOZA-8K', 'StarBED-8K']
const destinationNames = ['KAIT-8K-01', 'KAIT-8K-02', 'UDX-8K', 'OSAKA-8K']

/**
 * The TED has no branch VVF, so a headend sends one unicast stream: each IN can be ON AIR to one OUT
 * at a time (resource pool source_unicast). A branch VVF would raise this (v2).
 */
export const UNICASTS_PER_SOURCE = 1

/**
 * How a STANDBY stream waits for its Take (network operator config `limits.standby_feed`). `switcher`
 * (hot standby, the testbed): every STANDBY IN is already sent into a switcher that feeds its OUT, so
 * a Take only switches; the switcher and IN unicast pools are reserved at STANDBY. `none` (cold
 * standby): STANDBY holds only VVF nodes and the Take sets up the whole path.
 */
export const STANDBY_FEED: 'switcher' | 'none' = 'switcher'

export const sources: SourceNode[] = [1, 2, 3, 4].map((n) => ({
  id: `in-${n}`,
  name: sourceNames[n - 1],
  status: 'online',
  ...domainIn,
}))

export const destinations: DestinationNode[] = [1, 2, 3, 4].map((n) => ({
  id: `out-${n}`,
  name: destinationNames[n - 1],
  status: 'online',
  ...domainOut,
}))

export const functionCatalog: FunctionCatalogItem[] = [
  {
    type: 'caption',
    name: 'Caption',
    description: 'Overlay a caption on the video (8K DG only)',
    vvf_id: 'vvf-caption',
    default_params: { text: 'LIVE', position: 'bottom' },
    // The caption node only takes 8K DG (before transcode) and keeps it.
    format_transitions: { video_format: { '8k_dg': '8k_dg' } },
    param_schema: {
      type: 'object',
      required: ['text', 'position'],
      properties: {
        text: { type: 'string', maxLength: 64 },
        position: { type: 'string', enum: ['top', 'bottom'] },
      },
    },
  },
  {
    type: 'transcode',
    name: 'Transcode (8K DG to 8K FULL)',
    description: 'Convert 8K DG to 8K FULL',
    vvf_id: 'vvf-color-transcode-crop',
    default_params: {},
    param_schema: { type: 'object', properties: {} },
    format_transitions: { video_format: { '8k_dg': '8k_full' } },
  },
  {
    type: 'crop',
    name: 'Crop',
    description:
      'Cut an FHD region out of the 8K main stream as a side output (main stream unchanged)',
    vvf_id: 'vvf-color-transcode-crop',
    default_params: { x: 2880, y: 1620 },
    param_schema: {
      type: 'object',
      required: ['x', 'y'],
      properties: {
        x: {
          type: 'integer',
          minimum: 0,
          maximum: 5760,
          description: 'Top-left X of the FHD region (8K coordinates)',
        },
        y: {
          type: 'integer',
          minimum: 0,
          maximum: 3240,
          description: 'Top-left Y of the FHD region (8K coordinates)',
        },
      },
    },
  },
  {
    type: 'color',
    name: 'Color',
    description: 'Real-time RGB color modulation',
    vvf_id: 'vvf-color-transcode-crop',
    default_params: { r: 0, g: 0, b: 0 },
    param_schema: {
      type: 'object',
      required: ['r', 'g', 'b'],
      properties: {
        r: { type: 'integer', minimum: -255, maximum: 255 },
        g: { type: 'integer', minimum: -255, maximum: 255 },
        b: { type: 'integer', minimum: -255, maximum: 255 },
      },
    },
  },
]

/** Per-function processing latency used by the telemetry simulator (ms). */
export const processingLatencyMs: Record<FunctionType, number> = {
  color: 0.8,
  caption: 1.5,
  transcode: 6,
  delay: 0,
  crop: 1.2,
}

const now = new Date().toISOString()

// Chains follow the End.AN execution order (transcode -> crop -> color). Other orders are accepted
// (ordered: false) and reordered by the IEL before the routers.
export const presets: Preset[] = [
  {
    id: 'preset-1',
    name: 'Caption + Crop Center',
    description: 'Caption with the crop region at the center',
    chain: [
      {
        instance_id: 'sf-caption-1',
        type: 'caption',
        vvf_node_id: 'caption-1',
        params: { text: 'LIVE', position: 'bottom' },
      },
      { instance_id: 'sf-transcode-1', type: 'transcode', params: {} },
      { instance_id: 'sf-crop-1', type: 'crop', params: { x: 2880, y: 1620 } },
      { instance_id: 'sf-color-1', type: 'color', params: { r: 0, g: 0, b: 0 } },
    ],
    created_at: now,
    updated_at: now,
  },
  {
    id: 'preset-2',
    name: 'Caption + Crop Top-Left',
    description: 'Caption with the crop region at the top-left corner',
    chain: [
      {
        instance_id: 'sf-caption-1',
        type: 'caption',
        vvf_node_id: 'caption-2',
        params: { text: 'REPLAY', position: 'top' },
      },
      { instance_id: 'sf-transcode-1', type: 'transcode', params: {} },
      { instance_id: 'sf-crop-1', type: 'crop', params: { x: 0, y: 0 } },
      { instance_id: 'sf-color-1', type: 'color', params: { r: 32, g: 0, b: -32 } },
    ],
    created_at: now,
    updated_at: now,
  },
  {
    id: 'preset-3',
    name: 'Crop Center',
    description: 'Transcode / crop / color only',
    chain: [
      { instance_id: 'sf-transcode-1', type: 'transcode', params: {} },
      { instance_id: 'sf-crop-1', type: 'crop', params: { x: 2880, y: 1620 } },
      { instance_id: 'sf-color-1', type: 'color', params: { r: 0, g: -128, b: 255 } },
    ],
    created_at: now,
    updated_at: now,
  },
]

export const layouts: PresetLayout[] = [
  {
    preset_id: 'preset-1',
    nodes: [
      { node_id: 'in', position: { x: 250, y: 0 } },
      { node_id: 'sf-caption-1', position: { x: 250, y: 150 } },
      { node_id: 'sf-transcode-1', position: { x: 250, y: 300 } },
      { node_id: 'sf-crop-1', position: { x: 250, y: 450 } },
      { node_id: 'sf-color-1', position: { x: 250, y: 600 } },
      { node_id: 'out', position: { x: 250, y: 750 } },
    ],
  },
]

type CellSeed = { preset?: string; state: ExecutionState; health?: HealthState }

/** Initial cell states keyed by cell id (cells not listed start OFF). */
export const cellSeeds: Record<string, CellSeed> = {
  // Hot standby on the testbed: OUT 1 receives IN 1-3 through SW1 (three inputs), OUT 2 gets SW2 and
  // the only IN left (IN 4). OUT 3 / OUT 4 have no switcher and no IN left, so they cannot go STANDBY.
  'IN1-OUT1': { preset: 'preset-1', state: 'active' },
  // Pinned in REMEDIATING (blue / red blinking) so the rollback indicator can be seen in demos.
  'IN2-OUT1': { preset: 'preset-2', state: 'standby', health: 'remediating' },
  'IN3-OUT1': { preset: 'preset-1', state: 'standby', health: 'critical' },
  'IN4-OUT2': { preset: 'preset-3', state: 'active', health: 'warning' },
  'IN4-OUT3': { state: 'disconnected' },
}

export const defaultDynamicConfig: DynamicConfig = {
  sse_interval_ms: 1000,
  metrics_dump_interval_s: 30,
  log_level: 'info',
}
