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

/* The archive is navigated summary-first: /api/summary says what exists
   (channels, days, counts), and /api/clips is asked for one day at a time —
   at hundreds of clips a day, the full listing is a download nobody meant to
   start. channel may be '' (the clips nothing claims), so its presence, not
   its truthiness, is what filters. */
const clipQuery = ({ day, channel } = {}) => {
  const q = new URLSearchParams()
  if (day) q.set('day', day)
  if (channel != null) q.set('channel', channel)
  const qs = q.toString()
  return qs ? `?${qs}` : ''
}

export const api = {
  health: () => request('/api/health'),
  clips: (opts) => request(`/api/clips${clipQuery(opts)}`),
  summary: (channel) => request(`/api/summary${clipQuery({ channel })}`),
  sources: () => request('/api/sources'),
  addSource: (path) => request('/api/sources', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path }),
  }),
  removeSource: (path) => request(`/api/sources?path=${encodeURIComponent(path)}`, {
    method: 'DELETE',
  }),
  setChannelLabel: (channel, label) => request('/api/channels/label', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ channel, label }),
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

/* The browser-ready MP4 for formats a browser will not take directly (.dav
   above all): the server repackages the recording through ffmpeg into a cached,
   seekable MP4. With transcode set, the video stream is re-encoded to H.264 —
   the player's fallback for a codec this browser cannot decode. */
export const playURL = (clip, transcode) =>
  `/api/clip/play?source=${encodeURIComponent(clip.source)}&path=${encodeURIComponent(clip.path)}` +
  (transcode ? '&transcode=1' : '')
