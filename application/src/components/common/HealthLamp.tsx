import { RotateCw } from 'lucide-react'
import { useT, type MessageKey } from '../../i18n'
import { cx } from '../../lib/cx'
import type { HealthState } from '../../lib/types'

const labels: Record<HealthState, MessageKey> = {
  healthy: 'health.healthy',
  warning: 'health.warning',
  remediating: 'health.remediating',
  critical: 'health.critical',
}

export type HealthLampProps = { state: HealthState; className?: string }

/**
 * 4-state health lamp: blue / yellow / blue-red alternating / red-off blinking with "!".
 * Shape and icon differ per state so it does not rely on color alone.
 */
export function HealthLamp({ state, className }: HealthLampProps) {
  const t = useT()
  return (
    <span
      role="img"
      aria-label={t(labels[state])}
      title={t(labels[state])}
      className={cx(
        'inline-flex size-4 shrink-0 items-center justify-center rounded-full text-[0.625rem] leading-none font-black text-white',
        state === 'healthy' && 'bg-health-healthy',
        state === 'warning' && 'bg-health-warning',
        // Base colors stay visible when prefers-reduced-motion disables the animations.
        state === 'remediating' && 'animate-blue-red bg-health-remediating',
        state === 'critical' && 'animate-blink bg-health-critical',
        className,
      )}
    >
      {state === 'remediating' && <RotateCw className="size-2.5" aria-hidden />}
      {state === 'critical' && '!'}
    </span>
  )
}
