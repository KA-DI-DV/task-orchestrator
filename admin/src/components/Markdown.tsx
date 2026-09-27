import { useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

export function Markdown({ text }: { text: string }) {
  return (
    <div className="md">
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
    </div>
  )
}

// Довгий текст показуємо згорнутим, з кнопкою «Показати все».
export function Clamp({ text, limit = 700 }: { text: string; limit?: number }) {
  const [open, setOpen] = useState(false)
  const long = text.length > limit
  return (
    <>
      <div className={long ? `clamp ${open ? 'open' : ''}` : undefined}>
        <Markdown text={text} />
      </div>
      {long && (
        <button className="btn btn-ghost btn-sm more" onClick={() => setOpen(!open)}>
          {open ? 'Згорнути' : 'Показати все'}
        </button>
      )}
    </>
  )
}
