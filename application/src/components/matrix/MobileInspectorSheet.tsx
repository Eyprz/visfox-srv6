import { X } from 'lucide-react'
import { useT } from '../../i18n'
import { useSelectionStore } from '../../stores/selection'
import { CrosspointInspector } from './CrosspointInspector'

/**
 * Mobile (< md) inspector: a bottom sheet shown while a cell is selected.
 * On md and up the side InspectorPanel is used instead.
 */
export function MobileInspectorSheet() {
  const t = useT()
  const selected = useSelectionStore((s) => s.selectedCellId)
  const select = useSelectionStore((s) => s.select)
  if (!selected) return null
  return (
    <div
      role="dialog"
      aria-label={t('inspector.title')}
      className="border-line bg-surface fixed inset-x-0 bottom-0 z-40 flex max-h-[65vh] flex-col rounded-t-xl border-t shadow-2xl md:hidden"
    >
      <div className="border-line flex shrink-0 items-center justify-between border-b pl-4">
        <span className="text-ink-subtle text-[0.6875rem] font-semibold tracking-wider uppercase">
          {t('inspector.title')}
        </span>
        <button
          type="button"
          onClick={() => select(null)}
          aria-label={t('inspector.close')}
          className="text-ink-muted flex size-11 items-center justify-center"
        >
          <X className="size-5" aria-hidden />
        </button>
      </div>
      <div className="min-h-0 overflow-y-auto">
        <CrosspointInspector />
      </div>
    </div>
  )
}
