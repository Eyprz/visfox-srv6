import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../lib/api'
import type { CellPath } from '../../lib/types'
import { useMatrixStore } from '../../stores/matrix'
import { usePresetsStore } from '../../stores/presets'
import { useResourcesStore } from '../../stores/resources'
import { PathView } from './PathView'

const path: CellPath = {
  cell_id: 'IN1-OUT1',
  e2e_latency_ms: 25,
  nodes: [
    { node_id: 'in-1', role: 'headend', label: 'IN 1 (Headend)', router_id: 'rtr-01', sid: null },
    {
      node_id: 'caption-2',
      vvf_id: 'vvf-caption',
      role: 'vvf',
      label: 'Caption #2',
      instance_ids: ['sf-caption-1'],
      router_id: 'rtr-05',
      sid: 'fc00:1::103',
      processing_latency_ms: 1.5,
    },
    {
      node_id: 'out-1',
      role: 'egress',
      label: 'OUT 1 (Egress)',
      router_id: 'rtr-04',
      sid: 'fc00:1::200',
    },
  ],
  hops: [
    {
      from: 'in-1',
      to: 'caption-2',
      latency_ms: 12,
      packet_loss_rate: 0,
      bandwidth_utilization: 0.5,
    },
    {
      from: 'caption-2',
      to: 'out-1',
      latency_ms: 2,
      packet_loss_rate: 0,
      bandwidth_utilization: 0.4,
    },
  ],
  segment_list: ['fc00:1::103', 'fc00:1::200'],
  bottleneck: { kind: 'hop', ref: 'in-1->caption-2' },
}

beforeEach(() => {
  useResourcesStore.setState({
    sources: [{ id: 'in-1', name: 'KAIT-8K-01', status: 'online', video_format: '8k_dg' }],
    destinations: [{ id: 'out-1', name: 'UDX-8K', status: 'online', video_format: '8k_full' }],
  })
  useMatrixStore.setState({
    cells: {
      'IN1-OUT1': {
        cell_id: 'IN1-OUT1',
        source_id: 'in-1',
        destination_id: 'out-1',
        execution_state: 'active',
        health_state: 'warning',
        preset_name: 'Caption',
        live_chain: [{ instance_id: 'sf-caption-1', type: 'caption', params: {} }],
      },
    },
  })
  usePresetsStore.setState({ catalog: [{ type: 'caption', name: 'Caption overlay' }] as never })
})

describe('PathView', () => {
  it('shows the chain, the read-only L3 data, and the bottleneck by name', async () => {
    vi.spyOn(api, 'GET').mockResolvedValue({
      data: path,
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as never)
    render(
      <MemoryRouter>
        <PathView cellId="IN1-OUT1" />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Bottleneck: KAIT-8K-01 → Caption #2',
    )
    expect(screen.getAllByText('Caption #2').length).toBeGreaterThan(0)
    expect(screen.getByText('Caption overlay')).toBeInTheDocument()
    expect(screen.getAllByText('fc00:1::103').length).toBe(2)
    expect(screen.getByText('rtr-05')).toBeInTheDocument()
  })

  it('explains when the cell has no path', async () => {
    vi.spyOn(api, 'GET').mockResolvedValue({
      data: undefined,
      error: { code: 'INVALID_CELL_STATE', message: 'no path' },
      response: new Response(null, { status: 409 }),
    } as never)
    render(
      <MemoryRouter>
        <PathView cellId="IN1-OUT1" />
      </MemoryRouter>,
    )
    expect(await screen.findByText(/has no path/)).toBeInTheDocument()
  })
})
