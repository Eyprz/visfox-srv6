import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import type { MatrixCell } from '../../lib/types'
import { useMatrixStore } from '../../stores/matrix'
import { useSelectionStore } from '../../stores/selection'
import { CrosspointInspector } from './CrosspointInspector'

const cell = (state: MatrixCell['execution_state']): MatrixCell => ({
  cell_id: 'IN1-OUT1',
  source_id: 'in-1',
  destination_id: 'out-1',
  execution_state: state,
  health_state: state === 'active' || state === 'standby' ? 'healthy' : undefined,
  preset_id: state === 'off' ? null : 'p1',
  preset_name: state === 'off' ? null : 'Caption + Crop Center',
})

const renderFor = (state: MatrixCell['execution_state'] | null) => {
  useMatrixStore.setState({ cells: state ? { 'IN1-OUT1': cell(state) } : {} })
  useSelectionStore.setState({ selectedCellId: state ? 'IN1-OUT1' : null })
  return render(
    <MemoryRouter>
      <CrosspointInspector />
    </MemoryRouter>,
  )
}

beforeEach(() => useSelectionStore.setState({ selectedCellId: null }))

describe('CrosspointInspector actions by state', () => {
  it('shows a hint without a selection', () => {
    renderFor(null)
    expect(screen.getByText(/Select a crosspoint/)).toBeInTheDocument()
  })

  it('ACTIVE: SET STANDBY and OFF, no TAKE', () => {
    renderFor('active')
    expect(screen.getByRole('button', { name: 'SET STANDBY' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'OFF' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'TAKE' })).toBeNull()
  })

  it('STANDBY: TAKE and OFF', () => {
    renderFor('standby')
    expect(screen.getByRole('button', { name: 'TAKE' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'OFF' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'SET STANDBY' })).toBeNull()
  })

  it('OFF: preset selection only', () => {
    renderFor('off')
    expect(screen.queryByRole('button', { name: 'TAKE' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'OFF' })).toBeNull()
    expect(screen.getByRole('button', { name: /Select preset/ })).toBeInTheDocument()
  })

  it('shows HEALTH only for applied cells and no legend', () => {
    const { unmount } = renderFor('active')
    expect(screen.getByRole('heading', { name: 'Health' })).toBeInTheDocument()
    expect(screen.queryByText('Health legend')).toBeNull()
    unmount()
    for (const state of ['off', 'disconnected'] as const) {
      const r = renderFor(state)
      expect(screen.queryByRole('heading', { name: 'Health' })).toBeNull()
      r.unmount()
    }
  })
})
