/* The API, in one place. Every call returns parsed JSON or throws with the
   server's own error sentence, so components never parse a response twice or
   invent their own failure wording. */

async function request(path, options) {
  const res = await fetch(path, options)
  const body = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new Error(body.error || `${res.status} ${res.statusText}`)
  }
  return body
}

export const api = {
  health: () => request('/api/health'),
  clips: () => request('/api/clips'),
  storage: () => request('/api/storage'),
  saveConfig: (config) => request('/api/storage/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(config),
  }),
  enforce: (dryRun) => request(`/api/storage/enforce${dryRun ? '?dry_run=1' : ''}`, {
    method: 'POST',
  }),
}

/* Where a clip's bytes are served from — the <video> src and the download
   href alike. */
export const clipURL = (path) => `/api/clip?path=${encodeURIComponent(path)}`
