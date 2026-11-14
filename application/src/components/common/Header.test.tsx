import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { newlyCritical } from '../../lib/alertSound'
import { useConnectionStore } from '../../stores/connection'
import { useUiStore } from '../../stores/ui'
import { Header } from './Header'

const renderHeader = (path = '/') =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Header />
    </MemoryRouter>,
  )

describe('Header', () => {
  it('shows navigation, SSE status, and switches language', async () => {
    useConnectionStore.setState({ status: 'connected' })
    renderHeader('/topology/IN1-OUT1')
    expect(screen.getByRole('status')).toHaveTextContent('SSE: Connected')
    expect(screen.getByRole('link', { name: 'Topology' })).toHaveClass('bg-selected')

    await userEvent.click(screen.getByRole('button', { name: 'ja' }))
    expect(useUiStore.getState().locale).toBe('ja')
    expect(screen.getByRole('link', { name: 'マトリクス' })).toBeInTheDocument()
    useUiStore.setState({ locale: 'en' })
  })

  it('toggles the theme', async () => {
    renderHeader()
    await userEvent.click(screen.getAllByRole('button', { name: 'Switch to dark theme' })[0])
    expect(useUiStore.getState().theme).toBe('dark')
    useUiStore.setState({ theme: 'light' })
  })

  it('opens the sidebar with navigation and closes it on navigate', async () => {
    renderHeader()
    await userEvent.click(screen.getByRole('button', { name: 'Open menu' }))
    const drawer = screen.getByRole('dialog')
    await userEvent.click(within(drawer).getByRole('link', { name: 'Topology' }))
    expect(useUiStore.getState().sidebarOpen).toBe(false)
  })
})

describe('newlyCritical', () => {
  it('returns cells that just turned critical', () => {
    expect(
      newlyCritical(
        { a: 'healthy', b: 'critical' },
        { a: 'critical', b: 'critical', c: 'critical' },
      ),
    ).toEqual(['a', 'c'])
  })
})
