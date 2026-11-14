import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, ChevronRight } from 'lucide-react'
import type { ReactNode, RefObject } from 'react'
import { useNavigate } from 'react-router'
import { useT } from '../../i18n'
import { cx } from '../../lib/cx'
import type { MatrixCell } from '../../lib/types'
import { usePresetsStore } from '../../stores/presets'

/** Cell actions the menu dispatches; the owner (cell or inspector) holds the hooks and their dialogs. */
export type CellMenuActions = {
  take: () => void
  canTake: boolean
  apply: (presetId: string) => void
  setStandby: () => void
  turnOff: () => void
}

type CellMenuProps = {
  cell: MatrixCell
  actions: CellMenuActions
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Opens from this element (inspector [...] button). Without it the menu opens at `point`. */
  trigger?: ReactNode
  /**
   * Point to open at, relative to the positioned (relative) parent: the cell. Not viewport-fixed, because
   * the selected cell is transformed (scale), which would make it the containing block of a fixed anchor.
   */
  point?: { x: number; y: number }
  /** Element to focus when the menu closes (the cell), instead of the trigger. */
  returnFocusTo?: RefObject<HTMLElement | null>
}

// Run an action after the menu has closed, so a confirmation dialog it opens does not fight the menu for
// focus and pointer capture (Radix dialog opened from a closing dropdown).
const afterClose = (action: () => void) => () => setTimeout(action, 0)

const itemClass =
  'text-ink data-highlighted:bg-surface-muted data-disabled:text-ink-subtle flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm outline-none data-disabled:cursor-not-allowed'

/**
 * Crosspoint context menu: Take (STANDBY) / change preset / edit preset /
 * show path / SET STANDBY (ACTIVE) / OFF. Shared by the cell (right-click, Shift+F10, menu key) and the
 * inspector [...] button. Confirmations follow the same rules as the inspector buttons.
 */
export function CellMenu({
  cell,
  actions,
  open,
  onOpenChange,
  trigger,
  point,
  returnFocusTo,
}: CellMenuProps) {
  return (
    <DropdownMenu.Root open={open} onOpenChange={onOpenChange}>
      <DropdownMenu.Trigger asChild>
        {trigger ?? (
          // Zero-size anchor at the pointer (or the cell corner for keyboard) for the menu position.
          <span
            aria-hidden
            className="pointer-events-none absolute size-0"
            style={{ left: point?.x ?? 0, top: point?.y ?? 0 }}
          />
        )}
      </DropdownMenu.Trigger>
      {open && <CellMenuContent cell={cell} actions={actions} returnFocusTo={returnFocusTo} />}
    </DropdownMenu.Root>
  )
}

function CellMenuContent({
  cell,
  actions,
  returnFocusTo,
}: {
  cell: MatrixCell
  actions: CellMenuActions
  returnFocusTo?: RefObject<HTMLElement | null>
}) {
  const t = useT()
  const navigate = useNavigate()
  const presets = Object.values(usePresetsStore((s) => s.presets))
  const state = cell.execution_state
  const applied = state === 'active' || state === 'standby'

  return (
    <DropdownMenu.Portal>
      <DropdownMenu.Content
        // Name the menu itself; Radix would label it by the (hidden) trigger.
        aria-label={t('menu.label')}
        aria-labelledby={undefined}
        align="start"
        sideOffset={4}
        collisionPadding={8}
        // Menu clicks must not reach the matrix (deselect) or the cell (gestures) through the React tree.
        onClick={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onPointerDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
        onCloseAutoFocus={(e) => {
          const target = returnFocusTo?.current
          if (!target) return
          e.preventDefault()
          target.focus()
        }}
        className="border-line bg-surface z-40 min-w-56 rounded-md border p-1 shadow-lg"
      >
        {state === 'standby' && (
          <DropdownMenu.Item
            disabled={!actions.canTake}
            onSelect={afterClose(actions.take)}
            className={cx(itemClass, 'font-bold')}
          >
            {t('menu.take')}
          </DropdownMenu.Item>
        )}
        <DropdownMenu.Sub>
          <DropdownMenu.SubTrigger
            disabled={state === 'disconnected' || presets.length === 0}
            className={itemClass}
          >
            {t('menu.changePreset')}
            <ChevronRight className="ml-auto size-4" aria-hidden />
          </DropdownMenu.SubTrigger>
          <DropdownMenu.Portal>
            <DropdownMenu.SubContent
              sideOffset={4}
              collisionPadding={8}
              onClick={(e) => e.stopPropagation()}
              className="border-line bg-surface z-40 min-w-56 rounded-md border p-1 shadow-lg"
            >
              {presets.map((p) => (
                <DropdownMenu.Item
                  key={p.id}
                  onSelect={afterClose(() => actions.apply(p.id))}
                  className={itemClass}
                >
                  <Check
                    className={cx(
                      'size-4 shrink-0',
                      p.id === cell.preset_id ? 'opacity-100' : 'opacity-0',
                    )}
                    aria-hidden
                  />
                  {p.name}
                </DropdownMenu.Item>
              ))}
            </DropdownMenu.SubContent>
          </DropdownMenu.Portal>
        </DropdownMenu.Sub>
        <DropdownMenu.Item
          disabled={!cell.preset_id}
          onSelect={() => cell.preset_id && navigate(`/presets/${cell.preset_id}`)}
          className={itemClass}
        >
          {t('menu.editPreset')}
        </DropdownMenu.Item>
        <DropdownMenu.Item
          disabled={!applied}
          onSelect={() => navigate(`/topology/${cell.cell_id}`)}
          className={itemClass}
        >
          {t('menu.showPath')}
        </DropdownMenu.Item>
        {applied && <DropdownMenu.Separator className="bg-line my-1 h-px" />}
        {state === 'active' && (
          <DropdownMenu.Item onSelect={afterClose(actions.setStandby)} className={itemClass}>
            {t('menu.setStandby')}
          </DropdownMenu.Item>
        )}
        {applied && (
          <DropdownMenu.Item
            onSelect={afterClose(actions.turnOff)}
            className={cx(itemClass, state === 'active' && 'text-health-critical')}
          >
            {t('menu.off')}
          </DropdownMenu.Item>
        )}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  )
}
