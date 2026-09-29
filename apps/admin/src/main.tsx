import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Link, Route, Routes } from 'react-router'
import '@fontsource-variable/nunito'
import '@fontsource-variable/lora'
import '@fontsource-variable/jetbrains-mono'
import './styles.css'
import { Layout } from './components/Layout'
import { TasksPage } from './pages/TasksPage'
import { TaskPage } from './pages/TaskPage'
import { SettingsPage } from './pages/SettingsPage'

function NotFound() {
  return (
    <div className="page">
      <div className="card">
        <div className="empty">
          <h3>Такої сторінки немає</h3>
          <Link to="/" className="btn btn-soft">До списку задач</Link>
        </div>
      </div>
    </div>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <Layout>
        <Routes>
          <Route path="/" element={<TasksPage />} />
          <Route path="/tasks/:id" element={<TaskPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </Layout>
    </BrowserRouter>
  </StrictMode>,
)
