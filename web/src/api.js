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
  sources: () => request('/api/sources'),
  addSource: (path) => request('/api/sources', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path }),
  }),
  removeSource: (path) => request(`/api/sources?path=${encodeURIComponent(path)}`, {
    method: 'DELETE',
  }),
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
   href alike. A clip is addressed the way the listing handed it out: which
   source, and the path relative to it. */
export const clipURL = (clip) =>
  `/api/clip?source=${encodeURIComponent(clip.source)}&path=${encodeURIComponent(clip.path)}`

/* The remux stream for formats a browser will not take directly (.dav above
   all): the server repackages the recording through ffmpeg on the fly. */
export const playURL = (clip) =>
  `/api/clip/play?source=${encodeURIComponent(clip.source)}&path=${encodeURIComponent(clip.path)}`
