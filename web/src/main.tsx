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
import './index.css'

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
