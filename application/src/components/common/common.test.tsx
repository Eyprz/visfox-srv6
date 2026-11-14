import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { useUiStore } from '../../stores/ui'
import { ConfirmDialog } from './ConfirmDialog'
import { HealthLamp } from './HealthLamp'
import { StateBadge } from './StateBadge'

describe('ConfirmDialog', () => {
  it('focuses Cancel first and runs onConfirm only on explicit confirm', async () => {
    const user = userEvent.setup()
    const onConfirm = vi.fn()
    const onOpenChange = vi.fn()
    render(
      <ConfirmDialog
        open
        onOpenChange={onOpenChange}
        title="IN1-OUT1 is ON AIR"
        confirmLabel="Apply while on air"
        onConfirm={onConfirm}
      />,
    )
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onOpenChange).toHaveBeenCalledWith(false)

    await user.click(screen.getByRole('button', { name: 'Apply while on air' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })
})

describe('StateBadge', () => {
  it('renders the ON AIR label and becomes a button when clickable', async () => {
    const onClick = vi.fn()
    const { rerender } = render(<StateBadge state="active" />)
    expect(screen.getByText('ON AIR')).toBeInTheDocument()
    expect(screen.queryByRole('button')).toBeNull()
    rerender(<StateBadge state="standby" onClick={onClick} />)
    await userEvent.click(screen.getByRole('button', { name: 'STANDBY' }))
    expect(onClick).toHaveBeenCalled()
  })
})

describe('HealthLamp', () => {
  it('has a localized accessible label', () => {
    useUiStore.setState({ locale: 'ja' })
    render(<HealthLamp state="critical" />)
    expect(screen.getByRole('img', { name: '重大エラー' })).toBeInTheDocument()
    useUiStore.setState({ locale: 'en' })
  })
})
