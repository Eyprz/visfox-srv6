import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, ChevronDown } from 'lucide-react'
import { useT } from '../../i18n'
import { cx } from '../../lib/cx'
import { usePresetsStore } from '../../stores/presets'

export type PresetDropdownProps = {
  presetId: string | null | undefined
  presetName: string | null | undefined
  disabled?: boolean
  pending?: boolean
  onOpen?: () => void
  onSelect: (presetId: string) => void
  className?: string
}

/**
 * Base-colored (white) preset dropdown placed inside a state-colored matrix cell.
 * It is the only interactive child of the cell and stops click propagation so it never takes the cell.
 */
export function PresetDropdown({
  presetId,
  presetName,
  disabled = false,
  pending = false,
  onOpen,
  onSelect,
  className,
}: PresetDropdownProps) {
  const t = useT()
  const presets = Object.values(usePresetsStore((s) => s.presets))

  return (
    <DropdownMenu.Root onOpenChange={(open) => open && onOpen?.()}>
      <DropdownMenu.Trigger
        disabled={disabled || pending}
        onClick={(e) => e.stopPropagation()}
        // Keep cell gestures (long-press Take) off the dropdown.
        onPointerDown={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => e.stopPropagation()}
        aria-busy={pending || undefined}
        title={presetName ?? undefined}
        className={cx(
          'border-line bg-surface text-ink flex w-full min-w-0 items-center gap-1 rounded-md border px-2 py-1.5 text-left shadow-sm',
          'hover:bg-surface-muted focus-visible:outline-accent focus-visible:outline-2 focus-visible:outline-offset-1',
          'disabled:cursor-not-allowed disabled:opacity-60',
          pending && 'animate-pulse',
          className,
        )}
      >
        {/*
          Always two lines tall (2lh) so the state label above never moves with the name length:
          one-line names are vertically centered, longer names are clamped to two lines with an ellipsis.
        */}
        <span className="flex h-[2lh] min-w-0 flex-1 items-center text-[min(0.9375rem,12cqw)] leading-snug">
          <span
            className={cx(
              'line-clamp-2 break-words',
              presetName ? 'font-semibold' : 'text-ink-subtle',
            )}
          >
            {presetName ?? t('cell.selectPreset')}
          </span>
        </span>
        <ChevronDown className="text-ink-subtle size-4 shrink-0" aria-hidden />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          sideOffset={4}
          collisionPadding={8}
          className="border-line bg-surface z-40 min-w-56 rounded-md border p-1 shadow-lg"
          onClick={(e) => e.stopPropagation()}
        >
          {presets.map((p) => (
            <DropdownMenu.Item
              key={p.id}
              onSelect={() => onSelect(p.id)}
              className="text-ink data-highlighted:bg-surface-muted flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm outline-none"
            >
              <Check
                className={cx('size-4 shrink-0', p.id === presetId ? 'opacity-100' : 'opacity-0')}
                aria-hidden
              />
              {p.name}
            </DropdownMenu.Item>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
