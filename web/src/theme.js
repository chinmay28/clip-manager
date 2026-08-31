/* One palette for the whole app. The surfaces are the same deep navy family
   SAND Vault wears — the two apps are siblings and often run on the same box —
   but the accent is Clip Manager's own: the sky blue of the icon's timeline
   bars, with the recorder's red kept for errors and the REC dot alone. */
export const COLORS = {
  bg: '#0a0e17',
  surface: '#111827',
  surfaceHover: '#161f2f',
  surfaceRaised: '#1a2332',
  border: '#1e2d3d',
  borderBright: '#2b3f55',
  text: '#e2e8f0',
  textDim: '#94a3b8',
  textMuted: '#64748b',
  accent: '#0ea5e9',
  accentBright: '#38bdf8',
  accentDim: '#0369a1',
  error: '#ef4444',
  warn: '#eab308',
  success: '#22c55e',
  rec: '#ef4444',
}

export const FONT = {
  mono: "ui-monospace, 'SF Mono', 'JetBrains Mono', 'Fira Code', Menlo, Consolas, monospace",
  sans: "system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif",
  /* The wordmark's handwriting face. Caveat is the subset in web/fonts,
     embedded by the build when present (see vite.config.js); the rest are the
     platform's own script faces, so the mark degrades to handwriting rather
     than to body copy. */
  script: "Caveat, 'Snell Roundhand', 'Apple Chancery', 'Segoe Script', " +
    "'Ink Free', cursive",
}

/* Bytes, for humans. The API talks in bytes and nothing else — formatting is
   the client's job, in one place. */
export function formatBytes(n) {
  if (n == null) return '—'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let value = n
  let unit = -1
  do {
    value /= 1024
    unit++
  } while (value >= 1024 && unit < units.length - 1)
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[unit]}`
}
