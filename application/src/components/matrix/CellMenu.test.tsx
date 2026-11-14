import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../lib/api'
import type { MatrixCell } from '../../lib/types'
import { useMatrixStore } from '../../stores/matrix'
import { useSelectionStore } from '../../stores/selection'
import { CrosspointInspector } from './CrosspointInspector'
import { MatrixCellView } from './MatrixCellView'

const cell = (id: string, state: MatrixCell['execution_state']): MatrixCell => ({
  cell_id: id,
  source_id: 'in-1',
  destination_id: 'out-1',
  execution_state: state,
  health_state: state === 'active' || state === 'standby' ? 'healthy' : undefined,
  preset_id: state === 'active' || state === 'standby' ? 'p1' : null,
  preset_name: state === 'active' || state === 'standby' ? 'Crop Center' : null,
})
const ok = (data: unknown) => ({
  data,
  error: undefined,
  response: new Response(null, { status: 200 }),
})

beforeEach(() => {
  useSelectionStore.setState({ selectedCellId: null })
  useMatrixStore.setState({
    cells: {
      'IN1-OUT1': cell('IN1-OUT1', 'active'),
      'IN2-OUT1': cell('IN2-OUT1', 'standby'),
      'IN3-OUT1': cell('IN3-OUT1', 'off'),
    },
  })
})

const openMenu = async (cellId: string) => {
  render(
    <MemoryRouter>
      <MatrixCellView cellId={cellId} />
    </MemoryRouter>,
  )
  fireEvent.contextMenu(screen.getByRole('gridcell'))
  return screen.findByRole('menu', { name: 'Crosspoint actions' })
}
const item = (name: string) => screen.queryByRole('menuitem', { name })

describe('crosspoint context menu', () => {
  it('STANDBY: right-click selects the cell and offers Take and OFF', async () => {
    await openMenu('IN2-OUT1')
    expect(useSelectionStore.getState().selectedCellId).toBe('IN2-OUT1')
    for (const name of ['Take', 'Change preset', 'Edit preset', 'Show path', 'OFF (disconnect)'])
      expect(item(name)).toBeInTheDocument()
    expect(item('Stop output (SET STANDBY)')).toBeNull()
  })

  it('STANDBY: Take from the menu posts take', async () => {
    const post = vi
      .spyOn(api, 'POST')
      .mockResolvedValue(ok({ activated_cell: 'IN2-OUT1', deactivated_cell: null }) as never)
    vi.spyOn(api, 'GET').mockResolvedValue(ok(cell('IN2-OUT1', 'active')) as never)
    await openMenu('IN2-OUT1')
    await userEvent.click(item('Take')!)
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith('/matrix/cells/{cellId}/take', {
        params: { path: { cellId: 'IN2-OUT1' } },
      }),
    )
  })

  it('ACTIVE: SET STANDBY asks for confirmation, no Take', async () => {
    await openMenu('IN1-OUT1')
    expect(item('Take')).toBeNull()
    await userEvent.click(item('Stop output (SET STANDBY)')!)
    expect(await screen.findByRole('dialog')).toHaveTextContent('IN1-OUT1 is ON AIR')
  })

  it('OFF: no state actions; edit and path are disabled', async () => {
    await openMenu('IN3-OUT1')
    expect(item('Take')).toBeNull()
    expect(item('OFF (disconnect)')).toBeNull()
    expect(item('Edit preset')).toHaveAttribute('data-disabled')
    expect(item('Show path')).toHaveAttribute('data-disabled')
  })

  it('opens from the inspector [...] button', async () => {
    useSelectionStore.setState({ selectedCellId: 'IN2-OUT1' })
    vi.spyOn(api, 'GET').mockResolvedValue(ok(cell('IN2-OUT1', 'standby')) as never)
    render(
      <MemoryRouter>
        <CrosspointInspector />
      </MemoryRouter>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'More actions' }))
    expect(await screen.findByRole('menu', { name: 'Crosspoint actions' })).toBeInTheDocument()
    expect(item('Take')).toBeInTheDocument()
  })
})
