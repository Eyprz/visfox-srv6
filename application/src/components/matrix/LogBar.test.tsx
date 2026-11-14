import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import type { LogEntry } from '../../lib/types'
import { useLogsStore } from '../../stores/logs'
import { useSelectionStore } from '../../stores/selection'
import { LOG_BAR_LIMIT, LogBar } from './LogBar'

const entry = (n: number, level: LogEntry['level'], cell?: string): LogEntry => ({
  id: `log-${n}`,
  timestamp: new Date(Date.UTC(2026, 10, 5, 10, 0, n)).toISOString(),
  level,
  cell_id: cell ?? null,
  message: `message ${n}`,
})

beforeEach(() => {
  useSelectionStore.setState({ selectedCellId: null })
  // Newest first, like the store.
  useLogsStore.setState({
    entries: [
      entry(3, 'error', 'IN2-OUT2'),
      entry(2, 'warn'),
      entry(1, 'info'),
      ...Array.from({ length: 60 }, (_, i) => entry(100 + i, 'info')),
    ],
  })
})

describe('LogBar', () => {
  it('shows only the latest entry until expanded', () => {
    render(<LogBar />)
    expect(screen.getByText('message 3')).toBeInTheDocument()
    expect(screen.queryByText('message 2')).toBeNull()
  })

  it('expands to the newest entries and filters by level', async () => {
    render(<LogBar />)
    await userEvent.click(screen.getByRole('button', { expanded: false }))
    const list = screen.getByRole('list')
    expect(within(list).getAllByRole('listitem')).toHaveLength(LOG_BAR_LIMIT)
    await userEvent.click(screen.getByRole('radio', { name: 'WARN' }))
    expect(within(screen.getByRole('list')).getAllByRole('listitem')).toHaveLength(1)
    expect(within(screen.getByRole('list')).getByText('message 2')).toBeInTheDocument()
  })

  it('selects the cell of an entry', async () => {
    render(<LogBar />)
    await userEvent.click(screen.getByRole('button', { expanded: false }))
    await userEvent.click(screen.getByRole('button', { name: 'IN2-OUT2' }))
    expect(useSelectionStore.getState().selectedCellId).toBe('IN2-OUT2')
  })
})
