import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createBrowserRouter, RouterProvider } from 'react-router'
import App from './App'
import SourcesPage from './pages/SourcesPage'
import EditorPage from './pages/EditorPage'
import EntriesPage from './pages/EntriesPage'
import RunsPage from './pages/RunsPage'
import TargetsPage from './pages/TargetsPage'
import SettingsPage from './pages/SettingsPage'
import { applyTheme, loadThemeMode } from './theme'
import './index.css'

// 跟随系统时，操作系统切换深浅色要立刻反映到页面；手动主题不受影响。
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
  applyTheme(loadThemeMode())
})

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 10_000, retry: 1 } },
})

const router = createBrowserRouter([
  {
    path: '/',
    element: <App />,
    children: [
      { index: true, element: <SourcesPage /> },
      { path: 'sources/new', element: <EditorPage /> },
      { path: 'sources/:id', element: <EditorPage /> },
      { path: 'sources/:id/entries', element: <EntriesPage /> },
      { path: 'runs', element: <RunsPage /> },
      { path: 'targets', element: <TargetsPage /> },
      { path: 'settings', element: <SettingsPage /> },
    ],
  },
])

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
