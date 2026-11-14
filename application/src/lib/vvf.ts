import type { ChainConstraints, FunctionCatalogItem, FunctionInstance } from './types'

export type VvfGroup = {
  /** VVF id from the catalog (`vvf_id`), or the function type when the catalog does not know it. */
  vvfId: string
  /** VVF display name from the chain constraints, or the node's label for a pinned VVF. */
  name: string
  /** Node of a pinned VVF instance (`vvf_node_id`); null for dynamic VVFs (the IEL assigns it). */
  nodeId: string | null
  /** Display names of the functions in chain order. */
  functions: string[]
  /** Instance ids of the functions in chain order. */
  instanceIds: string[]
}

/**
 * Groups a chain into VVF instances for display (preset list, designer frames):
 * consecutive functions of one VVF, split where a type repeats or the pinned node changes.
 * VVF membership and names come from the IEL (`GET /functions` vvf_id and `GET /presets/constraints`
 * vvfs); nothing about the VVF layout is hard-coded here. Unknown types fall back to their own group.
 */
export function groupChainByVvf(
  chain: FunctionInstance[],
  catalog: FunctionCatalogItem[],
  constraints: ChainConstraints | null,
): VvfGroup[] {
  const groups: VvfGroup[] = []
  for (const fn of chain) {
    const item = catalog.find((c) => c.type === fn.type)
    const vvfId = item?.vvf_id ?? fn.type
    const fnName = item?.name ?? fn.type
    // Only pinned VVFs are told apart by node; the IEL assigns dynamic ones.
    const pinned = constraints?.vvfs.find((v) => v.id === vvfId)?.allocation === 'pinned'
    const nodeId = pinned ? (fn.vvf_node_id ?? null) : null
    const last = groups[groups.length - 1]
    // A repeated type starts a new VVF instance (two captions are two caption VVFs on two nodes).
    const repeated = last?.instanceIds.some(
      (id) => chain.find((c) => c.instance_id === id)?.type === fn.type,
    )
    if (last?.vvfId === vvfId && last.nodeId === nodeId && !repeated) {
      last.functions.push(fnName)
      last.instanceIds.push(fn.instance_id)
      continue
    }
    const vvfName =
      pinnedNodeLabel(constraints, vvfId, nodeId) ??
      constraints?.vvfs.find((v) => v.id === vvfId)?.name ??
      fnName
    groups.push({
      vvfId,
      name: vvfName,
      nodeId,
      functions: [fnName],
      instanceIds: [fn.instance_id],
    })
  }
  return groups
}

/** Label of a pinned VVF node from the IEL (`VvfDefinition.nodes`), or undefined. */
export function pinnedNodeLabel(
  constraints: ChainConstraints | null,
  vvfId: string | null | undefined,
  nodeId: string | null | undefined,
): string | undefined {
  if (!nodeId) return undefined
  const vvf = constraints?.vvfs.find((v) => v.id === vvfId)
  return vvf?.nodes?.find((n) => n.node_id === nodeId)?.label ?? nodeId
}

/**
 * Display name of a function: the catalog name, or for a function of a pinned VVF with a single
 * function type the node's label (e.g. "Caption: Logo"), since the node is what tells them apart.
 */
export function functionLabel(
  fn: FunctionInstance,
  catalog: FunctionCatalogItem[],
  constraints: ChainConstraints | null,
): string {
  const item = catalog.find((c) => c.type === fn.type)
  const name = item?.name ?? fn.type
  const vvf = constraints?.vvfs.find((v) => v.id === item?.vvf_id)
  const node = pinnedNodeLabel(constraints, item?.vvf_id, fn.vvf_node_id)
  if (!node) return name
  return vvf && vvf.function_types.length === 1 ? node : `${name} (${node})`
}
