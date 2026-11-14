import { createBrowserRouter } from 'react-router'
import { AppLayout } from './AppLayout'
import { DesignerPage } from './DesignerPage'
import { MatrixPage } from './MatrixPage'
import { PresetsPage } from './PresetsPage'
import { TopologyPage } from './TopologyPage'

// Routes follow
export const router = createBrowserRouter([
  {
    element: <AppLayout />,
    children: [
      { path: '/', element: <MatrixPage /> },
      { path: '/topology', element: <TopologyPage /> },
      { path: '/topology/:cellId', element: <TopologyPage /> },
      { path: '/presets', element: <PresetsPage /> },
      { path: '/presets/new', element: <DesignerPage /> },
      { path: '/presets/:presetId', element: <DesignerPage /> },
    ],
  },
])
