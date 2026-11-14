import { issueShortLabel, useT, type MessageKey } from '../../i18n'
import { useRef, useState } from 'react'
import { useApplyPreset } from '../../hooks/useApplyPreset'
import { useCellActions } from '../../hooks/useCellActions'
import { LONG_PRESS_MS, useLongPress } from '../../hooks/useLongPress'
import { useTake } from '../../hooks/useTake'
import { cx } from '../../lib/cx'
import type { ExecutionState, HealthState } from '../../lib/types'
import { useCell } from '../../stores/matrix'
import { useSelectionStore } from '../../stores/selection'
import { CellMenu } from './CellMenu'
import { PresetDropdown } from './PresetDropdown'

// The whole cell is the execution state indicator.
const cellStyles: Record<ExecutionState, string> = {
  active: 'bg-exec-active border-exec-active text-white',
  // STANDBY keeps the neutral frame (the perimeter is reserved for health errors and selection) and shows
  // its label as a green pill, so the fully filled ON AIR always stands out.
  standby: 'bg-surface border-slot text-ink',
  off: 'bg-surface border-slot text-ink-subtle',
  disconnected: 'bg-surface-muted border-slot text-ink-subtle hatch',
}
const stateLabels: Record<ExecutionState, MessageKey> = {
  active: 'exec.active',
  standby: 'exec.standby',
  off: 'exec.off',
  disconnected: 'exec.disconnected',
}
// Each error perimeter has a static ring as the base style and the blinking animation on top, so the
// ring stays visible even if the animation does not run.
const perimeter: Partial<Record<HealthState, string>> = {
  warning:
    'shadow-[0_0_0_3px_var(--color-surface),0_0_0_7px_var(--color-health-warning)] animate-perimeter-warning',
  remediating:
    'shadow-[0_0_0_3px_var(--color-surface),0_0_0_7px_var(--color-health-remediating)] animate-perimeter-remediating',
  critical:
    'shadow-[0_0_0_3px_var(--color-surface),0_0_0_7px_var(--color-health-critical)] animate-perimeter-critical',
}

/**
 * One crosspoint cell. The whole square is filled with the execution state color and acts as the
 * selection (and later Take) target; a base-colored preset dropdown sits at the bottom.
 * - health: blinking outer perimeter only on errors (box-shadow)
 * - selected: lifted (scale + drop shadow) with a white / accent / white frame inside the cell; it never meets the
 *   health ring that sits outside the cell
 * The cell is a div with button semantics because it contains the dropdown button.
 * Subscribes only to its own cell so telemetry updates re-render just this cell.
 */
export function MatrixCellView({ cellId }: { cellId: string }) {
  const t = useT()
  const cell = useCell(cellId)
  const selected = useSelectionStore((s) => s.selectedCellId === cellId)
  const select = useSelectionStore((s) => s.select)
  const { apply, pending, dialog } = useApplyPreset(cell)
  const { take, canTake, blockedBy, pending: taking, failed: takeFailed } = useTake(cell)
  // Gestures also run on a STANDBY cell that cannot be taken now: take() then explains why.
  const tryTake = cell?.execution_state === 'standby' && !taking
  // Touch long-press takes a STANDBY cell (PC uses double-click); the progress ring shows while held.
  const { handlers: longPress, pressing } = useLongPress(() => {
    select(cellId)
    void take()
  }, tryTake)
  // Pointer type of the last press, so double-click only takes for mouse (touch never uses double-tap).
  const lastPointer = useRef<string>('mouse')
  const { setStandby, turnOff, dialog: actionDialog } = useCellActions(cell)
  // Context menu position (right-click point, or the cell corner for Shift+F10 / the menu key).
  const [menuAt, setMenuAt] = useState<{ x: number; y: number } | null>(null)
  const cellRef = useRef<HTMLDivElement>(null)
  if (!cell) return <div role="gridcell" aria-disabled className="aspect-square" />

  const state = cell.execution_state
  const applied = state === 'active' || state === 'standby'
  // Health exists only for applied (active / standby) cells; the API omits it otherwise.
  const healthAnimation = applied && cell.health_state ? perimeter[cell.health_state] : undefined

  return (
    <div
      role="gridcell"
      tabIndex={0}
      aria-selected={selected}
      aria-label={`${cellId} ${t(stateLabels[state])}${blockedBy ? ` (${t('take.blocked')})` : ''} ${cell.preset_name ?? t('cell.noPreset')}`}
      data-cell-id={cellId}
      data-exec={state}
      data-health={cell.health_state}
      aria-busy={taking || undefined}
      ref={cellRef}
      onPointerDown={(e) => {
        // Ignore events bubbling through the React tree from portals (menus, dialogs).
        if (!e.currentTarget.contains(e.target as Node)) return
        lastPointer.current = e.pointerType
        longPress.onPointerDown(e)
      }}
      onPointerMove={longPress.onPointerMove}
      onPointerUp={longPress.onPointerUp}
      onPointerLeave={longPress.onPointerLeave}
      onPointerCancel={longPress.onPointerCancel}
      onContextMenu={(e) => {
        if (!e.currentTarget.contains(e.target as Node)) return
        e.preventDefault()
        // Touch long-press is Take; touch users open the menu from the inspector [...] instead.
        if (lastPointer.current === 'touch') return
        const rect = e.currentTarget.getBoundingClientRect()
        // Keyboard-triggered (Shift+F10 / menu key) events carry no pointer position inside the cell.
        const inside =
          e.clientX > rect.left &&
          e.clientX < rect.right &&
          e.clientY > rect.top &&
          e.clientY < rect.bottom
        select(cellId)
        // Anchor in cell coordinates (the cell may be scaled while selected).
        const scale = rect.width / (e.currentTarget.offsetWidth || rect.width)
        setMenuAt(
          inside
            ? { x: (e.clientX - rect.left) / scale, y: (e.clientY - rect.top) / scale }
            : { x: 8, y: 8 },
        )
      }}
      onClick={(e) => {
        if (e.currentTarget.contains(e.target as Node)) select(cellId)
      }}
      onDoubleClick={(e) => {
        if (!e.currentTarget.contains(e.target as Node)) return
        if (lastPointer.current === 'mouse' && tryTake) void take()
      }}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget) return
        if (e.key === 'Enter') {
          e.preventDefault()
          // Enter selects; on an already selected STANDBY cell it takes.
          if (selected && tryTake) void take()
          else select(cellId)
        } else if (e.key === ' ') {
          e.preventDefault()
          select(cellId)
        }
      }}
      className={cx(
        '@container relative flex aspect-square min-w-0 cursor-pointer touch-manipulation flex-col gap-2 rounded-md border-2 p-2 transition-[filter,scale] duration-150 select-none',
        'focus-visible:outline-accent hover:brightness-[1.04] focus-visible:outline-3 focus-visible:outline-offset-[-7px]',
        cellStyles[state],
        // Selected: lifted toward the viewer (scale + drop shadow, drawn above neighbors) and framed with a
        // white / accent / white line inside the cell (::after), so it reads on red ON AIR and white cells
        // alike and never touches the health ring outside the cell. No blinking (reserved for errors).
        selected &&
          'after:border-accent z-10 scale-[1.05] drop-shadow-[0_10px_18px_rgb(15_23_42/0.35)] after:pointer-events-none after:absolute after:inset-0 after:rounded-sm after:border-[3px] after:shadow-[inset_0_0_0_2px_white] after:content-[""]',
        healthAnimation,
        taking && 'animate-pulse',
      )}
    >
      {pressing && <LongPressRing />}
      {takeFailed && (
        <span
          role="status"
          className="bg-health-critical pointer-events-none absolute inset-x-2 top-2 z-10 rounded px-1 py-0.5 text-center text-[min(0.75rem,9cqw)] font-bold text-white uppercase"
        >
          {t('take.failed')}
        </span>
      )}
      <span
        className={cx(
          'flex min-h-0 flex-1 items-center justify-center text-center leading-none font-black tracking-wide uppercase',
          // The long DISCONNECTED label uses a smaller ratio so it never overflows the cell.
          state === 'disconnected' ? 'text-[min(1rem,10cqw)]' : 'text-[min(1.375rem,15cqw)]',
        )}
      >
        {state === 'standby' ? (
          <span className="flex flex-col items-center gap-[0.3em]">
            <span className="bg-exec-standby rounded-md px-[0.6em] py-[0.35em] text-white">
              {t(stateLabels[state])}
            </span>
            {/* Not takeable now (e.g. no free VVF node): the reason is in the title / inspector. */}
            {blockedBy && (
              <span
                title={blockedBy.message}
                className="text-ink-muted border-line-strong rounded border border-dashed px-[0.4em] py-[0.15em] text-[min(0.75rem,8cqw)] tracking-normal"
              >
                {issueShortLabel(t, blockedBy)}
              </span>
            )}
          </span>
        ) : (
          t(stateLabels[state])
        )}
      </span>
      <PresetDropdown
        presetId={cell.preset_id}
        presetName={cell.preset_name}
        disabled={state === 'disconnected'}
        pending={pending}
        onOpen={() => select(cellId)}
        onSelect={(presetId) => void apply(presetId)}
      />
      <CellMenu
        cell={cell}
        open={menuAt !== null}
        onOpenChange={(open) => !open && setMenuAt(null)}
        point={menuAt ?? undefined}
        returnFocusTo={cellRef}
        actions={{
          take: () => void take(),
          canTake,
          apply: (id) => void apply(id),
          setStandby,
          turnOff,
        }}
      />
      {dialog}
      {actionDialog}
    </div>
  )
}

/** Progress ring shown while a touch long-press is held; it fills in LONG_PRESS_MS. */
function LongPressRing() {
  return (
    <svg
      aria-hidden
      viewBox="0 0 100 100"
      className="pointer-events-none absolute inset-0 z-20 m-auto size-3/5 -rotate-90"
    >
      <circle cx="50" cy="50" r="44" fill="rgb(15 23 42 / 0.35)" />
      <circle
        cx="50"
        cy="50"
        r="44"
        fill="none"
        stroke="white"
        strokeWidth="8"
        strokeLinecap="round"
        pathLength="100"
        strokeDasharray="100"
        className="animate-[long-press_var(--long-press-ms)_linear_forwards]"
        style={{ ['--long-press-ms' as string]: `${LONG_PRESS_MS}ms` }}
      />
    </svg>
  )
}
