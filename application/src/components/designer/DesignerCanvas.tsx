import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position as Side,
  ReactFlow,
  useReactFlow,
  type Edge,
  type Node,
  type NodeChange,
  type EdgeChange,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useEffect, useMemo, useState, type DragEvent } from 'react'
import { useT, chainIssueKey } from '../../i18n'
import {
  canConnect,
  findInsertGap,
  flowingLinks,
  IN_ID,
  NODE_H,
  NODE_W,
  OUT_ID,
  walkChain,
  type PaletteEntry,
  type Position,
} from '../../lib/designer'
import { cx } from '../../lib/cx'
import { videoFormatLabel } from '../../lib/format'
import type { ChainIssue } from '../../lib/types'
import { functionLabel, groupChainByVvf } from '../../lib/vvf'
import { useDesignerStore } from '../../stores/designer'
import { usePresetsStore } from '../../stores/presets'
import { useUiStore } from '../../stores/ui'

const FRAME_PAD = 16
const FRAME_LABEL = 22

/** Drag payload type for palette -> canvas drops. */
export const PALETTE_MIME = 'application/x-visfox-functions'

/**
 * Types being dragged from the palette. dragover cannot read the payload, but the insertion point
 * depends on the VVF of the dragged functions, so the palette also records them here.
 */
export const paletteDrag: { types: string[] } = { types: [] }

type FnData = { title: string; subtitle: string; issues: string[]; locked: boolean }
type EndpointData = { label: string; format: string }
type FrameData = { label: string; issues: string[] }

function FunctionNode({ data, selected }: NodeProps<Node<FnData>>) {
  return (
    <div
      className={cx(
        'bg-surface text-ink flex h-full w-full flex-col justify-center rounded-md border-2 px-3',
        data.issues.length > 0 ? 'border-health-critical' : 'border-line-strong',
        selected && 'outline-accent outline-3 outline-offset-2',
      )}
      title={data.issues.join('\n') || undefined}
    >
      <Handle type="target" position={Side.Top} isConnectable={!data.locked} />
      <span className="truncate text-sm font-bold">{data.title}</span>
      <span className="text-ink-subtle truncate text-xs">{data.subtitle}</span>
      <Handle type="source" position={Side.Bottom} isConnectable={!data.locked} />
    </div>
  )
}

function EndpointNode({ id, data }: NodeProps<Node<EndpointData>>) {
  return (
    <div className="bg-selected text-on-selected flex h-full w-full flex-col items-center justify-center rounded-full">
      {id === OUT_ID && <Handle type="target" position={Side.Top} />}
      <span className="text-sm font-black tracking-wider">{data.label}</span>
      <span className="text-xs opacity-80">{data.format}</span>
      {id === IN_ID && <Handle type="source" position={Side.Bottom} />}
    </div>
  )
}

function VvfFrameNode({ data }: NodeProps<Node<FrameData>>) {
  return (
    <div
      className={cx(
        'pointer-events-none h-full w-full rounded-lg border-2 border-dashed',
        data.issues.length > 0
          ? 'border-health-critical bg-health-critical/5'
          : 'border-accent/60 bg-accent-soft/40',
      )}
    >
      <p className="text-ink-muted truncate px-2 pt-0.5 text-[0.6875rem] font-semibold tracking-wider uppercase">
        VVF: {data.label}
      </p>
      {data.issues.map((m) => (
        <p key={m} className="text-health-critical px-2 text-[0.6875rem] font-semibold">
          {m}
        </p>
      ))}
    </div>
  )
}

const nodeTypes = { fn: FunctionNode, endpoint: EndpointNode, vvf: VvfFrameNode }

/**
 * SFC designer canvas: IN -> functions -> OUT on a React Flow canvas.
 * Connections are checked with canConnect (linear, no loops) at connect time; functions of one VVF on
 * the chain are framed, and IEL issues are shown on their nodes / frames. Structural edits are
 * disabled while the preset is in use (`locked`). Must be inside a ReactFlowProvider.
 */
export function DesignerCanvas({ issues, locked }: { issues: ChainIssue[]; locked: boolean }) {
  const t = useT()
  const theme = useUiStore((s) => s.theme)
  const { screenToFlowPosition, fitView } = useReactFlow()
  const catalog = usePresetsStore((s) => s.catalog)
  const constraints = usePresetsStore((s) => s.constraints)
  const functions = useDesignerStore((s) => s.functions)
  const links = useDesignerStore((s) => s.links)
  const positions = useDesignerStore((s) => s.positions)
  const selectedId = useDesignerStore((s) => s.selectedId)
  const { move, select, connect, disconnect, removeFunction, addFunctions, dropNode } =
    useDesignerStore.getState()

  // Edge selection is local UI state (needed to delete a link with the Delete key).
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null)
  // While dragging over the line: the node the drop would be inserted after (its link is highlighted).
  const [insertAfter, setInsertAfter] = useState<string | null>(null)

  // VVF frames on the line, to keep their members together when inserting.
  const vvfOfType = (type: string) => catalog.find((c) => c.type === type)?.vvf_id ?? type
  const gapAt = (point: Position, types: string[], exclude?: string) =>
    findInsertGap(links, positions, point, {
      exclude,
      groups: groupChainByVvf(walkChain(functions, links).chain, catalog, constraints).map((g) => ({
        vvfId: g.vvfId,
        ids: g.instanceIds,
      })),
      inside: [...new Set(types.map(vvfOfType))],
    })

  // Keep the whole chain in view when functions are added or removed.
  const count = functions.length
  useEffect(() => {
    const id = requestAnimationFrame(
      () => void fitView({ padding: 0.3, maxZoom: 1, duration: 200 }),
    )
    return () => cancelAnimationFrame(id)
  }, [count, fitView])

  const nodes = useMemo(() => {
    const issueText = (i: ChainIssue) => t(chainIssueKey(i.code))
    const result: Node[] = []
    const domain = constraints?.domain
    result.push(
      endpoint(IN_ID, 'IN', domain ? videoFormatLabel(domain.source_format.video_format) : ''),
      endpoint(
        OUT_ID,
        'OUT',
        domain ? videoFormatLabel(domain.destination_format.video_format) : '',
      ),
    )
    function endpoint(id: string, label: string, format: string): Node<EndpointData> {
      return {
        id,
        type: 'endpoint',
        position: positions[id] ?? { x: 0, y: 0 },
        data: { label, format },
        width: NODE_W,
        height: NODE_H,
        deletable: false,
        selectable: false,
      }
    }
    // VVF frames around consecutive functions of one VVF on the chain.
    const { chain } = walkChain(functions, links)
    for (const group of groupChainByVvf(chain, catalog, constraints)) {
      const pts = group.instanceIds.map((id) => positions[id]).filter(Boolean)
      if (pts.length === 0) continue
      const minX = Math.min(...pts.map((p) => p.x))
      const minY = Math.min(...pts.map((p) => p.y))
      const maxX = Math.max(...pts.map((p) => p.x)) + NODE_W
      const maxY = Math.max(...pts.map((p) => p.y)) + NODE_H
      // VVF issues name the instance (its first function) when they concern one frame only.
      const vvfIssues = issues
        .filter(
          (i) =>
            i.vvf_id === group.vvfId &&
            (!i.instance_id || group.instanceIds.includes(i.instance_id)),
        )
        .map(issueText)
      result.push({
        id: `vvf:${group.instanceIds[0]}`,
        type: 'vvf',
        position: { x: minX - FRAME_PAD, y: minY - FRAME_PAD - FRAME_LABEL },
        data: { label: group.name, issues: vvfIssues },
        width: maxX - minX + FRAME_PAD * 2,
        height: maxY - minY + FRAME_PAD * 2 + FRAME_LABEL,
        draggable: false,
        selectable: false,
        deletable: false,
        focusable: false,
        zIndex: -1,
      })
    }
    for (const f of functions) {
      result.push({
        id: f.instance_id,
        type: 'fn',
        position: positions[f.instance_id] ?? { x: 0, y: 0 },
        data: {
          title: functionLabel(f, catalog, constraints),
          subtitle: f.instance_id,
          // VVF issues are shown on the frame, not on its first node.
          issues: issues.filter((i) => i.instance_id === f.instance_id && !i.vvf_id).map(issueText),
          locked,
        },
        width: NODE_W,
        height: NODE_H,
        selected: f.instance_id === selectedId,
        deletable: !locked,
      })
    }
    return result
  }, [catalog, constraints, functions, links, positions, selectedId, issues, locked, t])

  // Video flows only along the links reachable from IN: those get the moving dashes (source -> target);
  // dangling links keep the arrow but are muted and still.
  const flowing = new Set(flowingLinks(links).map((l) => `${l.source}->${l.target}`))
  const edges: Edge[] = links.map((l) => {
    const id = `${l.source}->${l.target}`
    const live = flowing.has(id)
    // The link a drop would split: thick, so the operator sees where the function goes.
    const target = insertAfter === l.source
    const color = live || target ? 'var(--color-accent)' : 'var(--color-slot)'
    return {
      id,
      source: l.source,
      target: l.target,
      deletable: !locked,
      selected: id === selectedEdge,
      className: live ? 'video-flow' : undefined,
      markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16, color },
      style: { strokeWidth: target ? 7 : 2.5, stroke: color },
    }
  })

  const onNodesChange = (changes: NodeChange[]) => {
    for (const c of changes) {
      if (c.type === 'position' && c.position) move(c.id, c.position)
      // Deselect only if it is still the selected node (changes in one batch may come in any order).
      else if (c.type === 'select') {
        if (c.selected) select(c.id)
        else if (useDesignerStore.getState().selectedId === c.id) select(null)
      }
    }
  }
  const onEdgesChange = (changes: EdgeChange[]) => {
    for (const c of changes) {
      if (c.type !== 'select') continue
      if (c.selected) setSelectedEdge(c.id)
      else setSelectedEdge((cur) => (cur === c.id ? null : cur))
    }
  }

  // Deletions go through onDelete, which reports deleted nodes and edges together: removing a node
  // bridges its neighbors, so the node's own edges must not be disconnected first.
  const onDelete = ({
    nodes: deletedNodes,
    edges: deletedEdges,
  }: {
    nodes: Node[]
    edges: Edge[]
  }) => {
    if (locked) return
    const removed = new Set(deletedNodes.map((n) => n.id))
    for (const e of deletedEdges) {
      if (!removed.has(e.source) && !removed.has(e.target)) disconnect(e.source, e.target)
    }
    for (const id of removed) removeFunction(id)
  }

  const onDrop = (e: DragEvent) => {
    const raw = e.dataTransfer.getData(PALETTE_MIME)
    if (!raw || locked) return
    e.preventDefault()
    const entries = JSON.parse(raw) as { type: string; vvf_node_id?: string }[]
    const types = entries.map((e) => e.type)
    const items: PaletteEntry[] = entries.flatMap((e) => {
      const item = catalog.find((c) => c.type === e.type)
      return item ? [{ ...item, vvf_node_id: e.vvf_node_id }] : []
    })
    const p = screenToFlowPosition({ x: e.clientX, y: e.clientY })
    // On a link of the line: wire in there; elsewhere: an unconnected node at the drop point.
    const after = gapAt(p, types)
    setInsertAfter(null)
    addFunctions(items, after ? { after } : { at: { x: p.x - NODE_W / 2, y: p.y - NODE_H / 2 } })
  }
  const nodeCenter = (n: Node) => ({ x: n.position.x + NODE_W / 2, y: n.position.y + NODE_H / 2 })
  const typeOf = (id: string) => functions.find((f) => f.instance_id === id)?.type

  return (
    <div
      className="min-h-0 min-w-0 flex-1"
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes(PALETTE_MIME) && !locked) {
          e.preventDefault()
          e.dataTransfer.dropEffect = 'copy'
          const p = screenToFlowPosition({ x: e.clientX, y: e.clientY })
          setInsertAfter(gapAt(p, paletteDrag.types))
        }
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as globalThis.Node | null))
          setInsertAfter(null)
      }}
      onDrop={onDrop}
    >
      <ReactFlow
        aria-label={t('designer.canvas')}
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onDelete={onDelete}
        // Dragging a node over the line shows where it would go; dropping wires it in there, or
        // puts the line back in its column.
        onNodeDrag={(_, n) => {
          if (locked || n.type !== 'fn') return
          const type = typeOf(n.id)
          setInsertAfter(gapAt(nodeCenter(n), type ? [type] : [], n.id))
        }}
        onNodeDragStop={(_, n) => {
          setInsertAfter(null)
          if (n.type !== 'fn' && n.type !== 'endpoint') return
          const type = typeOf(n.id)
          const after =
            !locked && n.type === 'fn' ? gapAt(nodeCenter(n), type ? [type] : [], n.id) : null
          dropNode(n.id, after)
        }}
        onConnect={(c) => c.source && c.target && connect(c.source, c.target)}
        isValidConnection={(c) => !locked && canConnect(links, c.source, c.target)}
        onPaneClick={() => select(null)}
        nodesConnectable={!locked}
        deleteKeyCode={locked ? null : ['Delete', 'Backspace']}
        colorMode={theme === 'dark' ? 'dark' : 'light'}
        fitView
        fitViewOptions={{ padding: 0.3, maxZoom: 1 }}
        proOptions={{ hideAttribution: true }}
      >
        <Background gap={24} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  )
}
