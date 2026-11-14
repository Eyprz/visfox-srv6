import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import type { Preset } from '../lib/types'
import { usePresetsStore } from '../stores/presets'
import { PresetsPage } from './PresetsPage'

const preset = (id: string, name: string, inUse: string[]): Preset =>
  ({
    id,
    name,
    chain: [{ instance_id: 'sf-crop-1', type: 'crop', params: {} }],
    in_use_cell_ids: inUse,
    updated_at: '2026-11-05T10:00:00Z',
  }) as Preset

function Where() {
  return <p data-testid="where">{useLocation().pathname}</p>
}
const renderPage = () =>
  render(
    <MemoryRouter initialEntries={['/presets']}>
      <Routes>
        <Route path="/presets" element={<PresetsPage />} />
        <Route path="*" element={<Where />} />
      </Routes>
    </MemoryRouter>,
  )

beforeEach(() =>
  usePresetsStore.setState({
    presets: {
      b: preset('b', 'Crop Center', ['IN1-OUT4', 'IN2-OUT4']),
      a: preset('a', 'Caption + Crop', []),
    },
    catalog: [{ type: 'crop', name: 'Crop', vvf_id: 'vvf-x' }] as never,
    constraints: { vvfs: [{ id: 'vvf-x', name: 'Color / Transcode / Crop' }] } as never,
  }),
)

describe('PresetsPage', () => {
  it('lists presets by name with the VVF summary and usage', () => {
    renderPage()
    const rows = screen.getAllByRole('row').slice(1)
    expect(rows.map((r) => r.querySelector('a')?.textContent)).toEqual([
      'Caption + Crop',
      'Crop Center',
    ])
    expect(rows[0]).toHaveTextContent('Not in use')
    expect(rows[1]).toHaveTextContent('2 cells')
    expect(rows[1]).toHaveTextContent('Color / Transcode / Crop: Crop')
  })

  it('opens the designer from a row and from New preset', async () => {
    const { unmount } = renderPage()
    await userEvent.click(screen.getAllByRole('row')[2])
    expect(screen.getByTestId('where')).toHaveTextContent('/presets/b')
    unmount()
    renderPage()
    await userEvent.click(screen.getByRole('button', { name: 'New preset' }))
    expect(screen.getByTestId('where')).toHaveTextContent('/presets/new')
  })
})
