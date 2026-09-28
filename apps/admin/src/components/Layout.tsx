import { useEffect, useState, type ReactNode } from 'react'
import { NavLink } from 'react-router'
import { LayoutList, Leaf, Moon, Sun } from 'lucide-react'

type Theme = 'light' | 'dark'

function initialTheme(): Theme {
  try {
    const saved = localStorage.getItem('theme')
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    // localStorage може бути недоступний (приватний режим) — беремо системну тему
  }
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function Layout({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>(initialTheme)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    try {
      localStorage.setItem('theme', theme)
    } catch {
      // не критично
    }
  }, [theme])

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark"><Leaf size={20} /></div>
          <div>
            <div className="brand-name">Orchestrator</div>
            <div className="brand-sub">Developer → Reviewer → QA</div>
          </div>
        </div>

        <nav className="nav">
          <NavLink to="/" end><LayoutList size={18} /> Задачі</NavLink>
        </nav>

        <div className="sidebar-foot">
          <button
            className="btn btn-soft btn-sm"
            onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
          >
            {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
            {theme === 'dark' ? 'Світла тема' : 'Темна тема'}
          </button>
          <div className="sidebar-note">Дані оновлюються автоматично кожні кілька секунд.</div>
        </div>
      </aside>

      <main className="main">{children}</main>
    </div>
  )
}
