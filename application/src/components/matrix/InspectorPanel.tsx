import { PanelRightClose, PanelRightOpen } from 'lucide-react'
import { useT } from '../../i18n'
import { cx } from '../../lib/cx'
import { useUiStore } from '../../stores/ui'
import { CrosspointInspector } from './CrosspointInspector'

/**
 * Right panel: crosspoint inspector. Collapsible to a thin rail so the matrix
 * can use the full width; the collapsed state is persisted with the other UI preferences.
 * Hidden below the md breakpoint: on mobile the inspector becomes a bottom sheet (next step).
 */
export function InspectorPanel() {
  const t = useT()
  const collapsed = useUiStore((s) => s.inspectorCollapsed)
  const setCollapsed = useUiStore((s) => s.setInspectorCollapsed)
  const toggleLabel = t(collapsed ? 'inspector.expand' : 'inspector.collapse')

  return (
    <aside
      aria-label={t('inspector.title')}
      data-collapsed={collapsed}
      className={cx(
        'border-line bg-surface hidden shrink-0 flex-col overflow-hidden border-l transition-[width] duration-200 md:flex',
        collapsed ? 'w-11' : 'w-80',
      )}
    >
      <div className="border-line flex h-11 shrink-0 items-center border-b">
        {!collapsed && (
          <h2 className="text-ink-subtle flex-1 truncate px-4 text-[0.6875rem] font-semibold tracking-wider uppercase">
            {t('inspector.title')}
          </h2>
        )}
        <button
          type="button"
          onClick={() => setCollapsed(!collapsed)}
          aria-expanded={!collapsed}
          aria-label={toggleLabel}
          title={toggleLabel}
          className="text-ink-muted hover:bg-surface-muted hover:text-ink flex h-full w-11 max-w-full shrink-0 items-center justify-center"
        >
          {collapsed ? (
            <PanelRightOpen className="size-5" aria-hidden />
          ) : (
            <PanelRightClose className="size-5" aria-hidden />
          )}
        </button>
      </div>
      {!collapsed && (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <CrosspointInspector />
        </div>
      )}
    </aside>
  )
}
