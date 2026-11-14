import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { clampZoom, useUiStore } from '../../stores/ui'
import { MatrixZoomControls } from './MatrixZoomControls'

afterEach(() => useUiStore.setState({ matrixZoom: 1 }))

describe('MatrixZoomControls', () => {
  it('zooms in / out in 10% steps and resets', async () => {
    render(<MatrixZoomControls />)
    await userEvent.click(screen.getByRole('button', { name: 'Zoom in' }))
    await userEvent.click(screen.getByRole('button', { name: 'Zoom in' }))
    expect(useUiStore.getState().matrixZoom).toBe(1.2)
    expect(screen.getByText('120%')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Zoom out' }))
    expect(useUiStore.getState().matrixZoom).toBe(1.1)
    await userEvent.click(screen.getByRole('button', { name: 'Reset zoom to 100%' }))
    expect(useUiStore.getState().matrixZoom).toBe(1)
  })

  it('clamps to 50% - 200% and disables the buttons at the limits', () => {
    expect(clampZoom(0.1)).toBe(0.5)
    expect(clampZoom(3)).toBe(2)
    useUiStore.setState({ matrixZoom: 2 })
    render(<MatrixZoomControls />)
    expect(screen.getByRole('button', { name: 'Zoom in' })).toBeDisabled()
  })

  it('persists the zoom to localStorage', () => {
    useUiStore.getState().setMatrixZoom(1.5)
    expect(JSON.parse(localStorage.getItem('visfox.ui')!).state.matrixZoom).toBe(1.5)
  })
})
