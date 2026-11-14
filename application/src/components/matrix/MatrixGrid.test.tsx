import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { axisIndexLabel, cellIdOf } from '../../lib/cellId'
import type { DestinationNode, SourceNode } from '../../lib/types'
import { useMatrixStore } from '../../stores/matrix'
import { useResourcesStore } from '../../stores/resources'
import { MAX_AXIS, MatrixGrid } from './MatrixGrid'

const src = (n: number): SourceNode => ({
  id: `in-${n}`,
  name: `Src ${n}`,
  status: 'online',
  video_format: '8k_dg',
})
const dst = (n: number): DestinationNode => ({
  id: `out-${n}`,
  name: `Dst ${n}`,
  status: 'online',
  video_format: '8k_full',
})
const range = (n: number) => Array.from({ length: n }, (_, i) => i + 1)

beforeEach(() => {
  useMatrixStore.setState({ cells: {}, loaded: true })
})

describe('cell id helpers', () => {
  it('follows the IN{n}-OUT{m} convention', () => {
    expect(cellIdOf('in-12', 'out-3')).toBe('IN12-OUT3')
  })

  it('derives the short axis label from the resource id', () => {
    expect(axisIndexLabel('in-3')).toBe('IN 3')
    expect(axisIndexLabel('out-12')).toBe('OUT 12')
  })
})

describe('MatrixGrid', () => {
  it('renders node names with the IN n / OUT n index', () => {
    useResourcesStore.setState({ sources: range(2).map(src), destinations: range(3).map(dst) })
    render(<MatrixGrid />)
    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).toEqual([
      'IN \\ OUT',
      'OUT 1Dst 1',
      'OUT 2Dst 2',
      'OUT 3Dst 3',
    ])
    expect(screen.getAllByRole('rowheader').map((h) => h.textContent)).toEqual([
      'IN 1Src 1',
      'IN 2Src 2',
    ])
  })

  it('caps each axis at MAX_AXIS and warns', () => {
    useResourcesStore.setState({
      sources: range(MAX_AXIS + 4).map(src),
      destinations: range(2).map(dst),
    })
    render(<MatrixGrid />)
    expect(screen.getAllByRole('rowheader')).toHaveLength(MAX_AXIS)
    expect(screen.getByRole('alert')).toHaveTextContent(
      `Showing the first ${MAX_AXIS} of ${MAX_AXIS + 4} IN.`,
    )
  })

  it('shows an empty state without resources', () => {
    useResourcesStore.setState({ sources: [], destinations: [] })
    render(<MatrixGrid />)
    expect(screen.getByText('No inputs or outputs are registered.')).toBeInTheDocument()
  })
})

describe('MatrixGrid selection', () => {
  it('clears the selection when clicking outside cells or pressing Escape', async () => {
    const { useSelectionStore } = await import('../../stores/selection')
    const userEvent = (await import('@testing-library/user-event')).default
    useResourcesStore.setState({ sources: range(1).map(src), destinations: range(1).map(dst) })
    useMatrixStore.setState({
      cells: {
        'IN1-OUT1': {
          cell_id: 'IN1-OUT1',
          source_id: 'in-1',
          destination_id: 'out-1',
          execution_state: 'standby',
          health_state: 'healthy',
        },
      },
    })
    render(<MatrixGrid />)
    await userEvent.click(screen.getByText('STANDBY'))
    expect(useSelectionStore.getState().selectedCellId).toBe('IN1-OUT1')
    await userEvent.click(screen.getByText('IN \\ OUT'))
    expect(useSelectionStore.getState().selectedCellId).toBeNull()

    await userEvent.click(screen.getByText('STANDBY'))
    await userEvent.click(screen.getByRole('button', { name: 'Zoom in' }))
    expect(useSelectionStore.getState().selectedCellId).toBe('IN1-OUT1')
    await userEvent.keyboard('{Escape}')
    expect(useSelectionStore.getState().selectedCellId).toBeNull()
  })
  it('moves the selection and focus with arrow keys, clamped at the edges', async () => {
    const { useSelectionStore } = await import('../../stores/selection')
    const userEvent = (await import('@testing-library/user-event')).default
    useResourcesStore.setState({ sources: range(2).map(src), destinations: range(2).map(dst) })
    const cells = Object.fromEntries(
      range(2).flatMap((i) =>
        range(2).map((o) => [
          cellIdOf(`in-${i}`, `out-${o}`),
          {
            cell_id: cellIdOf(`in-${i}`, `out-${o}`),
            source_id: `in-${i}`,
            destination_id: `out-${o}`,
            execution_state: 'off' as const,
            health_state: 'healthy' as const,
          },
        ]),
      ),
    )
    useMatrixStore.setState({ cells })
    const { container } = render(<MatrixGrid />)
    const cell = (id: string) => container.querySelector<HTMLElement>(`[data-cell-id="${id}"]`)!
    useSelectionStore.getState().select(null)
    cell('IN1-OUT1').focus()

    await userEvent.keyboard('{ArrowRight}')
    expect(useSelectionStore.getState().selectedCellId).toBe('IN1-OUT2')
    expect(document.activeElement).toBe(cell('IN1-OUT2'))
    await userEvent.keyboard('{ArrowRight}')
    expect(useSelectionStore.getState().selectedCellId).toBe('IN1-OUT2')
    await userEvent.keyboard('{ArrowDown}')
    expect(useSelectionStore.getState().selectedCellId).toBe('IN2-OUT2')
    await userEvent.keyboard('{ArrowLeft}{ArrowUp}{ArrowUp}')
    expect(useSelectionStore.getState().selectedCellId).toBe('IN1-OUT1')
    expect(document.activeElement).toBe(cell('IN1-OUT1'))
  })
})
