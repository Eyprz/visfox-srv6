import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../lib/api'
import type { MatrixCell } from '../../lib/types'
import { useMatrixStore } from '../../stores/matrix'
import { useSelectionStore } from '../../stores/selection'
import { MatrixCellView } from './MatrixCellView'

const cell = (id: string, state: MatrixCell['execution_state']): MatrixCell => ({
  cell_id: id,
  source_id: 'in-1',
  destination_id: 'out-1',
  execution_state: state,
  health_state: 'healthy',
})
const ok = (data: unknown) => ({
  data,
  error: undefined,
  response: new Response(null, { status: 200 }),
})

beforeEach(() => {
  useSelectionStore.setState({ selectedCellId: null })
  useMatrixStore.setState({
    cells: { 'IN3-OUT1': cell('IN3-OUT1', 'standby'), 'IN1-OUT1': cell('IN1-OUT1', 'active') },
  })
})

describe('MatrixCellView take', () => {
  it('takes a STANDBY cell on mouse double-click and refreshes both cells', async () => {
    const post = vi
      .spyOn(api, 'POST')
      .mockResolvedValue(ok({ activated_cell: 'IN3-OUT1', deactivated_cell: 'IN1-OUT1' }) as never)
    const get = vi.spyOn(api, 'GET').mockImplementation((async (
      _path: string,
      init: { params: { path: { cellId: string } } },
    ) => {
      const id = init.params.path.cellId
      return ok(cell(id, id === 'IN3-OUT1' ? 'active' : 'standby'))
    }) as never)
    render(<MatrixCellView cellId="IN3-OUT1" />)
    await userEvent.dblClick(screen.getByRole('gridcell'))
    expect(post).toHaveBeenCalledWith('/matrix/cells/{cellId}/take', {
      params: { path: { cellId: 'IN3-OUT1' } },
    })
    expect(get).toHaveBeenCalledTimes(2)
    expect(useMatrixStore.getState().cells['IN3-OUT1'].execution_state).toBe('active')
    expect(useMatrixStore.getState().cells['IN1-OUT1'].execution_state).toBe('standby')
  })

  it('does not take an ON AIR cell', async () => {
    const post = vi.spyOn(api, 'POST')
    render(<MatrixCellView cellId="IN1-OUT1" />)
    await userEvent.dblClick(screen.getByRole('gridcell'))
    expect(post).not.toHaveBeenCalled()
  })

  it('flashes TAKE FAILED when the take fails', async () => {
    vi.spyOn(api, 'POST').mockResolvedValue({
      data: undefined,
      error: { code: 'ROUTER_ERROR', message: 'Router returned an error' },
      response: new Response(null, { status: 502 }),
    } as never)
    render(<MatrixCellView cellId="IN3-OUT1" />)
    await userEvent.dblClick(screen.getByRole('gridcell'))
    expect(await screen.findByText('TAKE FAILED')).toBeInTheDocument()
  })
})
