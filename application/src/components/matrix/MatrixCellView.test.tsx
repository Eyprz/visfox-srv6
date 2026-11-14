import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import type { MatrixCell } from '../../lib/types'
import { useMatrixStore } from '../../stores/matrix'
import { useSelectionStore } from '../../stores/selection'
import { MatrixCellView } from './MatrixCellView'

const base: MatrixCell = {
  cell_id: 'IN1-OUT1',
  source_id: 'in-1',
  destination_id: 'out-1',
  execution_state: 'active',
  health_state: 'healthy',
  preset_name: 'Caption + Crop Center',
}

const renderCell = (cell: Partial<MatrixCell>) => {
  useMatrixStore.setState({ cells: { 'IN1-OUT1': { ...base, ...cell } } })
  return render(<MatrixCellView cellId="IN1-OUT1" />)
}
const cellButton = () => screen.getByRole('gridcell')

beforeEach(() => {
  useMatrixStore.setState({ cells: {} })
  useSelectionStore.setState({ selectedCellId: null })
})

describe('MatrixCellView', () => {
  it('fills the cell with the state and shows the preset dropdown, with no health indicator when healthy', () => {
    renderCell({})
    expect(cellButton()).toHaveTextContent('ON AIR')
    expect(cellButton()).toHaveTextContent('Caption + Crop Center')
    expect(cellButton().className).not.toMatch(/animate-perimeter/)
    expect(cellButton()).toHaveClass('bg-exec-active')
    expect(screen.getByRole('button', { name: /Caption \+ Crop Center/ })).toBeInTheDocument()
    expect(screen.queryByRole('img')).toBeNull()
  })

  it('blinks the perimeter only on health errors', () => {
    const { unmount } = renderCell({ health_state: 'warning' })
    expect(cellButton()).toHaveClass('animate-perimeter-warning')
    unmount()
    const r = renderCell({ health_state: 'remediating' })
    expect(cellButton()).toHaveClass('animate-perimeter-remediating')
    r.unmount()
    renderCell({ health_state: 'critical' })
    expect(cellButton()).toHaveClass('animate-perimeter-critical')
  })

  it('shows "No preset" for OFF and hatches DISCONNECTED', () => {
    const { unmount } = renderCell({ execution_state: 'off', preset_name: null })
    expect(cellButton()).toHaveTextContent('Select preset')
    unmount()
    renderCell({ execution_state: 'disconnected', preset_name: null })
    expect(cellButton()).toHaveClass('hatch')
    expect(screen.getByRole('button', { name: /Select preset/ })).toBeDisabled()
  })

  it('selects the cell on click', async () => {
    renderCell({})
    await userEvent.click(screen.getByText('ON AIR'))
    expect(useSelectionStore.getState().selectedCellId).toBe('IN1-OUT1')
    expect(cellButton()).toHaveAttribute('aria-selected', 'true')
  })
})
