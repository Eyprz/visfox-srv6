import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { useUiStore } from '../../stores/ui'
import { InspectorPanel } from './InspectorPanel'

afterEach(() => useUiStore.setState({ inspectorCollapsed: false }))

describe('InspectorPanel', () => {
  it('collapses to a rail and expands again', async () => {
    render(
      <MemoryRouter>
        <InspectorPanel />
      </MemoryRouter>,
    )
    const panel = screen.getByRole('complementary', { name: 'Crosspoint inspector' })
    await userEvent.click(screen.getByRole('button', { name: 'Collapse panel' }))
    expect(useUiStore.getState().inspectorCollapsed).toBe(true)
    expect(panel).toHaveAttribute('data-collapsed', 'true')
    expect(screen.queryByRole('heading')).toBeNull()

    await userEvent.click(screen.getByRole('button', { name: 'Expand panel' }))
    expect(useUiStore.getState().inspectorCollapsed).toBe(false)
    expect(screen.getByRole('heading', { name: 'Crosspoint inspector' })).toBeInTheDocument()
  })
})
