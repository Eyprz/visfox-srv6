import { useParams } from 'react-router'
import { CellList } from '../components/topology/CellList'
import { PathView } from '../components/topology/PathView'
import { TopologyOverview } from '../components/topology/TopologyOverview'

/**
 * L7 / L3 topology map and real path view: the cells with a path on the left;
 * the abstract topology at /topology, or the selected cell's real path at /topology/:cellId.
 */
export function TopologyPage() {
  const { cellId } = useParams()
  return (
    <div className="flex min-h-0 flex-1">
      <CellList />
      {cellId ? <PathView key={cellId} cellId={cellId} /> : <TopologyOverview />}
    </div>
  )
}
