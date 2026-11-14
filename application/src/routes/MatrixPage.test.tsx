import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { MatrixPage } from './MatrixPage'

describe('MatrixPage layout', () => {
  it('has the matrix, preset panel, and log bar regions', () => {
    render(
      <MemoryRouter>
        <MatrixPage />
      </MemoryRouter>,
    )
    expect(screen.getByRole('region', { name: 'Crosspoint matrix' })).toBeInTheDocument()
    expect(screen.getByRole('complementary', { name: 'Crosspoint inspector' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'System log' })).toBeInTheDocument()
  })
})
