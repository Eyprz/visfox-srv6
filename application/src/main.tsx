import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from 'react-router'
import { loadFonts } from './lib/fonts'
import { applyTheme } from './lib/theme'
import { router } from './routes/router'
import { useUiStore } from './stores/ui'
import './index.css'

async function enableMocking() {
  // VITE_API_MOCK=true serves /api/v1 from the in-browser mock IEL (src/mocks) until the Go IEL exists.
  if (import.meta.env.VITE_API_MOCK !== 'true') return
  const { startMockIel } = await import('./mocks/browser')
  await startMockIel()
}

// Apply the saved theme before the first paint so a dark-theme user never sees a light flash.
applyTheme(useUiStore.getState().theme)

// Render only after the mock and the fonts are ready, so the first paint already uses Play / M PLUS 2.
Promise.all([enableMocking(), loadFonts(useUiStore.getState().locale)]).then(() => {
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <RouterProvider router={router} />
    </StrictMode>,
  )
})
