import { LogBar } from '../components/matrix/LogBar'
import { MatrixGrid } from '../components/matrix/MatrixGrid'
import { InspectorPanel } from '../components/matrix/InspectorPanel'
import { MobileInspectorSheet } from '../components/matrix/MobileInspectorSheet'

// Main screen: matrix | crosspoint inspector, with the log bar along the bottom.
// Regions are separated by rules, not cards.
export function MatrixPage() {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-0 flex-1">
        <MatrixGrid />
        <InspectorPanel />
      </div>
      <LogBar />
      <MobileInspectorSheet />
    </div>
  )
}
