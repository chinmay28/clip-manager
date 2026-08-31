import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { api, clipURL, playURL } from './api'
import { COLORS, FONT, formatBytes } from './theme'
import { Brand, DevMark } from './components/Brand'

/* The app is two answers behind two tabs: what the cameras have recorded
   (home — the clips and nothing else), and how the app is set up (Settings —
   sources and quotas). Playing a clip hangs off the first; drawing a quota and
   running a cleanup off the second. */
export function App() {
  const [tab, setTab] = useState('clips')
  const [clips, setClips] = useState(null)
  const [labels, setLabels] = useState({})
  const [storage, setStorage] = useState(null)
  const [sources, setSources] = useState(null)
  const [error, setError] = useState(null)
  const [playing, setPlaying] = useState(null)

  const refresh = useCallback(async () => {
    try {
      const [c, s, src] = await Promise.all([api.clips(), api.storage(), api.sources()])
      setClips(c.clips || [])
      setLabels(c.channel_labels || {})
      setStorage(s)
      setSources(src.sources || [])
      setError(null)
    } catch (e) {
      setError(e.message)
    }
  }, [])

  useEffect(() => { refresh() }, [refresh])

  return (
    <Shell>
      <header style={{
        display: 'flex',
        alignItems: 'center',
        gap: '12px',
        padding: '14px 18px',
        borderBottom: `1px solid ${COLORS.border}`,
      }}>
        <Brand />
        <span style={{ flex: 1 }} />
        <nav style={{ display: 'flex', gap: '4px' }}>
          <Tab active={tab === 'clips'} onClick={() => setTab('clips')}>Clips</Tab>
          <Tab active={tab === 'settings'} onClick={() => setTab('settings')}>Settings</Tab>
        </nav>
        <DevMark />
      </header>

      <main style={{ maxWidth: '860px', margin: '0 auto', padding: '18px 18px 48px' }}>
        {error && (
          <div style={{
            padding: '12px 14px',
            marginBottom: '16px',
            borderRadius: '8px',
            border: `1px solid ${COLORS.error}`,
            color: COLORS.error,
            fontSize: '14px',
          }}>{error}</div>
        )}

        {tab === 'clips' && clips && (
          <ClipList
            clips={clips}
            labels={labels}
            onPlay={setPlaying}
            onChanged={refresh}
            // Naming the source on every row only earns its ink once there
            // is more than one place a clip could have come from.
            showSource={(sources || []).length > 1}
          />
        )}
        {tab === 'settings' && (
          <>
            {sources && storage && (
              <SourcesPanel sources={sources} usage={storage.usage} onChanged={refresh} />
            )}
            {storage && <StoragePanel storage={storage} onChanged={refresh} />}
          </>
        )}
        {!clips && !error && (
          <p style={{ color: COLORS.textMuted }}>Loading…</p>
        )}
      </main>

      {playing && <Player clip={playing} onClose={() => setPlaying(null)} />}
    </Shell>
  )
}

function Tab({ active, onClick, children }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        padding: '7px 14px',
        borderRadius: '8px',
        border: `1px solid ${active ? COLORS.accentDim : 'transparent'}`,
        background: active ? `${COLORS.accent}22` : 'none',
        color: active ? COLORS.accentBright : COLORS.textDim,
        fontSize: '14px',
        cursor: 'pointer',
      }}
    >{children}</button>
  )
}

/* ------------------------------------------------------------------------- */
/* Sources: the directories the clips come from                              */
/* ------------------------------------------------------------------------- */

/* The last path segment is usually the name a person knows the directory by;
   the full path stays in the row's title and beside it in the muted text. */
const baseName = (p) => p.split('/').filter(Boolean).pop() || p

function SourcesPanel({ sources, usage, onChanged }) {
  const [path, setPath] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)

  const add = async () => {
    if (!path.trim()) return
    setBusy(true)
    setError(null)
    try {
      await api.addSource(path.trim())
      setPath('')
      await onChanged()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const remove = async (src) => {
    setBusy(true)
    setError(null)
    try {
      await api.removeSource(src)
      await onChanged()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section style={{
      marginBottom: '26px',
      padding: '16px',
      borderRadius: '10px',
      border: `1px solid ${COLORS.border}`,
      background: COLORS.surface,
    }}>
      <h2 style={{ margin: '0 0 10px', fontSize: '15px', fontWeight: 600 }}>Sources</h2>

      {sources.map((s) => {
        const su = (usage.sources || {})[s.path]
        return (
          <div key={s.path} title={s.path} style={{
            display: 'flex',
            alignItems: 'center',
            gap: '10px',
            padding: '7px 0',
            borderTop: `1px solid ${COLORS.border}`,
            fontSize: '13px',
          }}>
            <span style={{ minWidth: 0, flex: 1 }}>
              <span style={{ color: COLORS.text }}>{baseName(s.path)}</span>
              <span style={{
                color: COLORS.textMuted,
                marginLeft: '8px',
                fontFamily: FONT.mono,
                fontSize: '12px',
                wordBreak: 'break-all',
              }}>{s.path}</span>
            </span>
            {!s.available ? (
              /* An unplugged drive or a NAS that is down: the source stays
                 listed — forgetting it would read as deleted footage — and
                 the row says why its clips are not in the list right now. */
              <span style={{ color: COLORS.warn }}>not readable right now</span>
            ) : (
              <span style={{ color: COLORS.textDim, fontFamily: FONT.mono }}>
                {su ? <>{formatBytes(su.bytes)}<span style={{ color: COLORS.textMuted }}> · {su.files}</span></> : '—'}
              </span>
            )}
            {s.pinned ? (
              /* Named on the service's command line: the app shows it but
                 cannot remove it — what the operator pinned outranks a
                 click. */
              <span title="Named on the service's command line" style={{
                fontFamily: FONT.mono,
                fontSize: '11px',
                color: COLORS.textMuted,
                border: `1px solid ${COLORS.border}`,
                borderRadius: '4px',
                padding: '2px 6px',
              }}>pinned</span>
            ) : (
              <button type="button" onClick={() => remove(s.path)} disabled={busy} style={buttonStyle()}>
                Remove
              </button>
            )}
          </div>
        )
      })}

      <div style={{ display: 'flex', gap: '8px', marginTop: '10px', flexWrap: 'wrap' }}>
        <input
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') add() }}
          placeholder="/path/to/footage"
          style={{ ...inputStyle, width: 'min(340px, 100%)', textAlign: 'left' }}
        />
        <button type="button" onClick={add} disabled={busy || !path.trim()} style={buttonStyle(true)}>
          Add source
        </button>
      </div>
      <p style={{ color: COLORS.textMuted, fontSize: '12px', margin: '8px 0 0' }}>
        An absolute path on the machine the server runs on, laid out one
        subdirectory per camera. Removing a source deletes nothing — it only
        takes the directory out of view and out from under the quotas.
      </p>

      {error && <p style={{ color: COLORS.error, fontSize: '13px', margin: '10px 0 0' }}>{error}</p>}
    </section>
  )
}

/* ------------------------------------------------------------------------- */
/* Storage: usage against the lines drawn over it                            */
/* ------------------------------------------------------------------------- */

const GB = 1024 * 1024 * 1024

/* Quotas are typed in gigabytes because that is the unit anyone thinks about
   footage in; the API speaks bytes and nothing else. An empty field is "no
   line drawn", which is different from zero and must survive the round trip. */
const toGB = (bytes) => (bytes > 0 ? String(Math.round((bytes / GB) * 100) / 100) : '')
const fromGB = (text) => {
  const v = parseFloat(text)
  return Number.isFinite(v) && v > 0 ? Math.round(v * GB) : 0
}

function StoragePanel({ storage, onChanged }) {
  const { usage, config } = storage
  const [quotaGB, setQuotaGB] = useState(toGB(config.quota_bytes))
  const [cameraGB, setCameraGB] = useState(() => {
    const init = {}
    for (const [name, q] of Object.entries(config.camera_quota_bytes || {})) {
      init[name] = toGB(q)
    }
    return init
  })
  const [busy, setBusy] = useState(false)
  const [report, setReport] = useState(null)
  const [error, setError] = useState(null)

  const cameras = Object.keys(usage.cameras || {}).sort()

  const save = async () => {
    setBusy(true)
    setError(null)
    try {
      const camera_quota_bytes = {}
      for (const [name, text] of Object.entries(cameraGB)) {
        const bytes = fromGB(text)
        if (bytes > 0) camera_quota_bytes[name] = bytes
      }
      await api.saveConfig({ quota_bytes: fromGB(quotaGB), camera_quota_bytes })
      await onChanged()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const enforce = async (dryRun) => {
    setBusy(true)
    setError(null)
    try {
      setReport(await api.enforce(dryRun))
      if (!dryRun) await onChanged()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  const quota = config.quota_bytes
  const over = quota > 0 && usage.bytes > quota

  return (
    <section style={{
      marginBottom: '26px',
      padding: '16px',
      borderRadius: '10px',
      border: `1px solid ${COLORS.border}`,
      background: COLORS.surface,
    }}>
      <h2 style={{ margin: '0 0 10px', fontSize: '15px', fontWeight: 600 }}>Storage</h2>

      <div style={{ display: 'flex', alignItems: 'baseline', gap: '8px', flexWrap: 'wrap' }}>
        <span style={{ fontFamily: FONT.mono, fontSize: '20px' }}>{formatBytes(usage.bytes)}</span>
        <span style={{ color: COLORS.textMuted, fontSize: '13px' }}>
          in {usage.files} clip{usage.files === 1 ? '' : 's'}
          {quota > 0 && <> · quota {formatBytes(quota)}</>}
        </span>
        {over && <span style={{ color: COLORS.warn, fontSize: '13px' }}>over quota</span>}
      </div>

      {quota > 0 && (
        <UsageBar fraction={usage.bytes / quota} over={over} />
      )}

      {(usage.missing || []).length > 0 && (
        /* A total that silently shrank because a disk went away would read
           as deleted footage — say which sources the figures do not cover. */
        <p style={{ color: COLORS.warn, fontSize: '13px', margin: '10px 0 0' }}>
          Not counted — unreadable right now: {usage.missing.join(', ')}
        </p>
      )}

      <div style={{
        display: 'flex',
        alignItems: 'center',
        gap: '8px',
        marginTop: '14px',
        flexWrap: 'wrap',
      }}>
        <label style={{ fontSize: '13px', color: COLORS.textDim }}>
          Directory quota{' '}
          <input
            value={quotaGB}
            onChange={(e) => setQuotaGB(e.target.value)}
            placeholder="none"
            inputMode="decimal"
            style={inputStyle}
          /> GB
        </label>
        <button type="button" onClick={save} disabled={busy} style={buttonStyle(true)}>
          Save quotas
        </button>
        <span style={{ flex: 1 }} />
        <button type="button" onClick={() => enforce(true)} disabled={busy} style={buttonStyle()}>
          Preview cleanup
        </button>
        <button type="button" onClick={() => enforce(false)} disabled={busy} style={buttonStyle()}>
          Enforce now
        </button>
      </div>

      {cameras.length > 0 && (
        <table style={{ width: '100%', marginTop: '14px', borderCollapse: 'collapse', fontSize: '13px' }}>
          <tbody>
            {cameras.map((name) => {
              const cu = usage.cameras[name]
              return (
                <tr key={name} style={{ borderTop: `1px solid ${COLORS.border}` }}>
                  <td style={{ padding: '7px 8px 7px 0', color: COLORS.text }}>
                    {name || <em style={{ color: COLORS.textMuted }}>no camera</em>}
                  </td>
                  <td style={{ padding: '7px 8px', color: COLORS.textDim, fontFamily: FONT.mono }}>
                    {formatBytes(cu.bytes)}
                    <span style={{ color: COLORS.textMuted }}> · {cu.files}</span>
                  </td>
                  <td style={{ padding: '7px 0', textAlign: 'right', color: COLORS.textDim }}>
                    {/* Files with no camera directory have no directory a
                        per-camera quota could meter — only the global line
                        covers them. */}
                    {name && (
                      <>
                        quota{' '}
                        <input
                          value={cameraGB[name] ?? ''}
                          onChange={(e) => setCameraGB({ ...cameraGB, [name]: e.target.value })}
                          placeholder="none"
                          inputMode="decimal"
                          style={inputStyle}
                        /> GB
                      </>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}

      {error && <p style={{ color: COLORS.error, fontSize: '13px', margin: '10px 0 0' }}>{error}</p>}
      {report && <EnforceReport report={report} onDismiss={() => setReport(null)} />}
    </section>
  )
}

function UsageBar({ fraction, over }) {
  return (
    <div style={{
      marginTop: '10px',
      height: '8px',
      borderRadius: '4px',
      background: COLORS.surfaceRaised,
      overflow: 'hidden',
    }}>
      <div style={{
        width: `${Math.min(fraction * 100, 100)}%`,
        height: '100%',
        borderRadius: '4px',
        background: over ? COLORS.warn : COLORS.accent,
        transition: 'width 300ms ease',
      }} />
    </div>
  )
}

/* What a cleanup did — or, on a dry run, would do. Deleting footage silently
   is the one thing this app must never be caught doing, so the report names
   every file and which line claimed it. */
function EnforceReport({ report, onDismiss }) {
  const verb = report.dry_run ? 'would delete' : 'deleted'
  return (
    <div style={{
      marginTop: '12px',
      padding: '10px 12px',
      borderRadius: '8px',
      border: `1px solid ${COLORS.border}`,
      background: COLORS.surfaceRaised,
      fontSize: '13px',
    }}>
      <div style={{ display: 'flex', alignItems: 'baseline' }}>
        <strong style={{ fontWeight: 600 }}>
          {report.dry_run ? 'Dry run: ' : ''}
          {verb} {report.deleted.length} clip{report.deleted.length === 1 ? '' : 's'}
          {' '}({formatBytes(report.freed_bytes)})
        </strong>
        <span style={{ flex: 1 }} />
        <button type="button" onClick={onDismiss} style={buttonStyle()}>Dismiss</button>
      </div>
      {report.deleted.length > 0 && (
        <ul style={{
          margin: '8px 0 0',
          paddingLeft: '18px',
          color: COLORS.textDim,
          fontFamily: FONT.mono,
          fontSize: '12px',
          maxHeight: '180px',
          overflowY: 'auto',
        }}>
          {report.deleted.map((d) => (
            <li key={`${d.source}:${d.path}`}>{d.path} — {formatBytes(d.size)} ({d.rule} quota)</li>
          ))}
        </ul>
      )}
      {(report.failed || []).map((f) => (
        <p key={f} style={{ color: COLORS.error, margin: '6px 0 0' }}>could not remove {f}</p>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------------- */
/* The clips themselves                                                      */
/* ------------------------------------------------------------------------- */

/* When a recording started, as a Date. The server reads it out of the file
   name (upload time lies by minutes); mod time is the fallback of last
   resort. */
const startOf = (clip) => new Date(clip.start_time || clip.mod_time)

/* The name a channel wears when nobody has labeled it yet: the raw key,
   readably — "N843A8_ch3" → "N843A8 ch3". */
const channelFallback = (key) => (key ? key.replace(/_/g, ' ') : 'no channel')
const channelName = (key, labels) => labels[key] || channelFallback(key)

const sameDay = (a, b) =>
  a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()

/* "Today" and "Yesterday" are how anyone actually thinks about camera
   footage; every earlier day gets its full name. */
function dayLabel(date) {
  const now = new Date()
  if (sameDay(date, now)) return 'Today'
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1)
  if (sameDay(date, yesterday)) return 'Yesterday'
  return date.toLocaleDateString(undefined, {
    weekday: 'long', year: 'numeric', month: 'short', day: 'numeric',
  })
}

/* The clip browser: channels across the top, days down the page. A channel is
   what the recordings themselves say they belong to (parsed from Dahua-style
   names, with the camera directory as fallback), and the user can label one
   with a name that means something — "Front door" instead of "N843A8 ch3". */
function ClipList({ clips, labels, onPlay, onChanged, showSource }) {
  const [channel, setChannel] = useState(null)

  const channels = useMemo(
    () => [...new Set(clips.map((c) => c.channel))].sort(),
    [clips],
  )

  // Days newest first, clips newest first within each — by when the
  // recording started, not when its upload finished. The API's oldest-first
  // order belongs to enforcement, not to people.
  const days = useMemo(() => {
    const filtered = channel == null ? clips : clips.filter((c) => c.channel === channel)
    const sorted = [...filtered].sort((a, b) => startOf(b) - startOf(a))
    const out = []
    for (const clip of sorted) {
      const date = startOf(clip)
      const last = out[out.length - 1]
      if (last && sameDay(last.date, date)) {
        last.clips.push(clip)
      } else {
        out.push({ date, clips: [clip] })
      }
    }
    return out
  }, [clips, channel])

  if (clips.length === 0) {
    return (
      <p style={{ color: COLORS.textMuted, fontSize: '14px' }}>
        No clips yet. Point your cameras (or their NVR) at the clips directory —
        one subdirectory per camera — and they will appear here.
      </p>
    )
  }

  return (
    <section>
      {(channels.length > 1 || channels[0] !== '') && (
        <div style={{
          display: 'flex',
          gap: '6px',
          flexWrap: 'wrap',
          alignItems: 'center',
          marginBottom: '12px',
        }}>
          <Chip active={channel == null} onClick={() => setChannel(null)}>All</Chip>
          {channels.map((key) => (
            <Chip key={key} active={channel === key} onClick={() => setChannel(key)}>
              {channelName(key, labels)}
            </Chip>
          ))}
          {channel != null && channel !== '' && (
            <ChannelRename channel={channel} label={labels[channel] || ''} onChanged={onChanged} />
          )}
        </div>
      )}

      {days.map(({ date, clips: dayClips }) => (
        <div key={date.toDateString()} style={{ marginBottom: '18px' }}>
          <h3 style={{
            margin: '0 0 8px',
            fontSize: '13px',
            fontWeight: 600,
            color: COLORS.textDim,
            textTransform: 'uppercase',
            letterSpacing: '0.04em',
          }}>
            {dayLabel(date)}
            <span style={{ color: COLORS.textMuted, fontWeight: 400, textTransform: 'none', letterSpacing: 0 }}>
              {' '}· {dayClips.length} clip{dayClips.length === 1 ? '' : 's'}
            </span>
          </h3>
          <div style={{ borderRadius: '10px', border: `1px solid ${COLORS.border}`, overflow: 'hidden' }}>
            {dayClips.map((clip) => (
              <ClipRow
                key={`${clip.source}:${clip.path}`}
                clip={clip}
                onPlay={onPlay}
                showSource={showSource}
                // Inside a filtered channel every row would repeat the chip
                // above it; only the "All" view needs the name per row.
                channelName={channel == null ? channelName(clip.channel, labels) : null}
              />
            ))}
          </div>
        </div>
      ))}
    </section>
  )
}

/* Labeling the selected channel, inline beside its chip: a pencil affordance,
   an input, done. The label lives in the server's config, so every browser
   sees the same names. */
function ChannelRename({ channel, label, onChanged }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(label)
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setBusy(true)
    try {
      await api.setChannelLabel(channel, draft.trim())
      await onChanged()
      setEditing(false)
    } catch {
      // refresh() surfaced the error banner; stay in the editor
    } finally {
      setBusy(false)
    }
  }

  if (!editing) {
    return (
      <button
        type="button"
        onClick={() => { setDraft(label); setEditing(true) }}
        style={buttonStyle()}
      >✏️ Rename</button>
    )
  }
  return (
    <span style={{ display: 'inline-flex', gap: '6px', alignItems: 'center' }}>
      <input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => { if (e.key === 'Enter') save() }}
        placeholder={channelFallback(channel)}
        autoFocus
        style={{ ...inputStyle, width: '150px', textAlign: 'left' }}
      />
      <button type="button" onClick={save} disabled={busy} style={buttonStyle(true)}>Save</button>
      <button type="button" onClick={() => setEditing(false)} disabled={busy} style={buttonStyle()}>Cancel</button>
    </span>
  )
}

function Chip({ active, onClick, children }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        padding: '5px 12px',
        borderRadius: '999px',
        border: `1px solid ${active ? COLORS.accent : COLORS.border}`,
        background: active ? `${COLORS.accent}22` : 'none',
        color: active ? COLORS.accentBright : COLORS.textDim,
        fontSize: '13px',
        cursor: 'pointer',
      }}
    >{children}</button>
  )
}

function ClipRow({ clip, onPlay, showSource, channelName }) {
  const when = startOf(clip)
  return (
    <div style={{
      display: 'flex',
      alignItems: 'center',
      gap: '10px',
      padding: '9px 12px',
      borderBottom: `1px solid ${COLORS.border}`,
      background: COLORS.surface,
      fontSize: '13px',
    }}>
      <span style={{ minWidth: 0, flex: 1 }}>
        <div style={{
          color: COLORS.text,
          whiteSpace: 'nowrap',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
        }}>
          {/* The day is the group header; the time is what tells this clip
              from its neighbors, so the time leads the row. */}
          <span style={{ fontFamily: FONT.mono }}>{when.toLocaleTimeString()}</span>
          {channelName && <span style={{ color: COLORS.textDim }}> · {channelName}</span>}
        </div>
        <div style={{
          color: COLORS.textMuted,
          fontSize: '12px',
          whiteSpace: 'nowrap',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
        }} title={clip.name}>
          {formatBytes(clip.size)}
          {showSource && <> · <span title={clip.source}>{baseName(clip.source)}</span></>}
          {' · '}{clip.name}
        </div>
      </span>
      {clip.playable || clip.remuxable ? (
        <>
          {clip.remuxable && (
            /* The badge stays on a remuxed format: the play works because
               the server repackages it, and naming the container keeps
               "why does this one buffer differently" answerable. */
            <span style={{
              fontFamily: FONT.mono,
              fontSize: '11px',
              color: COLORS.textMuted,
              border: `1px solid ${COLORS.border}`,
              borderRadius: '4px',
              padding: '2px 6px',
            }}>{clip.ext.replace('.', '')}</span>
          )}
          <button type="button" onClick={() => onPlay(clip)} style={buttonStyle(true)}>
            ▶ Play
          </button>
        </>
      ) : (
        /* No ffmpeg on the server (or a format nothing repackages): the
           honest offer is the bytes themselves. The badge names the format
           so the difference doesn't read as a bug. */
        <>
          <span style={{
            fontFamily: FONT.mono,
            fontSize: '11px',
            color: COLORS.textMuted,
            border: `1px solid ${COLORS.border}`,
            borderRadius: '4px',
            padding: '2px 6px',
          }}>{clip.ext.replace('.', '')}</span>
          <a href={clipURL(clip)} download={clip.name} style={{
            ...buttonStyle(),
            textDecoration: 'none',
            display: 'inline-flex',
            alignItems: 'center',
          }}>↓ Download</a>
        </>
      )}
    </div>
  )
}

/* The player, over everything. Native controls: seeking, volume and
   fullscreen are the browser's job. A natively playable clip is served as-is
   with range support; a repackaged one (.dav) is a cached MP4 served the same
   way — both scrub.

   Playback failure gets a ladder before it gets a message: the file as-is
   (when the browser should take it), then the server's remux, then the
   server's H.264 transcode — the one every browser decodes. Only when all of
   that fails does the player admit defeat, as a sentence with a way out, not
   a black rectangle. */
function Player({ clip, onClose }) {
  // Every URL worth trying, in order. Each onError steps down one rung.
  const attempts = useMemo(() => {
    const list = []
    if (clip.playable) list.push(clipURL(clip))
    if (clip.playable || clip.remuxable) {
      list.push(playURL(clip))
      list.push(playURL(clip, true))
    }
    return list.length > 0 ? list : [clipURL(clip)]
  }, [clip])
  const [attempt, setAttempt] = useState(0)
  const failed = attempt >= attempts.length

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div
      onClick={onClose}
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 100,
        display: 'grid',
        placeItems: 'center',
        padding: '20px',
        background: 'rgba(10, 14, 23, 0.88)',
      }}
    >
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(92vw, 900px)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '8px' }}>
          <span style={{
            color: COLORS.text,
            fontSize: '14px',
            minWidth: 0,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}>{clip.name}</span>
          <span style={{ flex: 1 }} />
          <a href={clipURL(clip)} download={clip.name} style={{
            ...buttonStyle(),
            textDecoration: 'none',
            display: 'inline-flex',
            alignItems: 'center',
          }}>↓ Download</a>
          <button type="button" onClick={onClose} style={buttonStyle()}>Close</button>
        </div>
        {failed ? (
          <div style={{
            padding: '28px 20px',
            borderRadius: '8px',
            border: `1px solid ${COLORS.border}`,
            background: COLORS.surface,
            color: COLORS.textDim,
            fontSize: '14px',
            textAlign: 'center',
          }}>
            This clip would not play even after converting it on the server —
            the recording may be damaged, or the server has no ffmpeg.
            Download it and try VLC.
          </div>
        ) : (
          <video
            key={attempts[attempt]}
            src={attempts[attempt]}
            controls
            autoPlay
            playsInline
            onError={() => setAttempt(attempt + 1)}
            style={{ width: '100%', borderRadius: '8px', background: '#000' }}
          />
        )}
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------------- */
/* Chrome                                                                    */
/* ------------------------------------------------------------------------- */

const inputStyle = {
  width: '64px',
  padding: '5px 7px',
  borderRadius: '6px',
  border: `1px solid ${COLORS.border}`,
  background: COLORS.bg,
  color: COLORS.text,
  fontFamily: FONT.mono,
  fontSize: '13px',
  textAlign: 'right',
}

const buttonStyle = (primary) => ({
  padding: '6px 12px',
  borderRadius: '6px',
  border: `1px solid ${primary ? COLORS.accentDim : COLORS.border}`,
  background: primary ? `${COLORS.accent}26` : COLORS.surfaceRaised,
  color: primary ? COLORS.accentBright : COLORS.textDim,
  fontSize: '13px',
  cursor: 'pointer',
})

function Shell({ children }) {
  return (
    <div style={{
      minHeight: '100vh',
      background: COLORS.bg,
      color: COLORS.text,
      fontFamily: FONT.sans,
    }}>
      <style>{`
        @keyframes clip-dev-veil {
          0% { opacity: 0; }
          7% { opacity: 1; }
          82% { opacity: 1; }
          100% { opacity: 0; }
        }
        /* Lands with a small overshoot, then drifts out as the veil clears. */
        @keyframes clip-dev-badge {
          0% { transform: scale(0.82); }
          14% { transform: scale(1.02); }
          22% { transform: scale(1); }
          82% { transform: scale(1); }
          100% { transform: scale(1.06); }
        }
        /* The cross-fade stays — it isn't motion — but the scale doesn't. */
        @media (prefers-reduced-motion: reduce) {
          .clip-dev-lockup { animation: none !important; }
        }
        * { box-sizing: border-box; }
        :root { color-scheme: dark; }
        html { -webkit-text-size-adjust: 100%; }
        body { margin: 0; background: ${COLORS.bg}; }
        /* iOS zooms the whole page in when it focuses a field smaller than
           16px. Every input here is styled inline, so this is one of the few
           places that has to shout to win. */
        @media (max-width: 860px) {
          input, textarea, select { font-size: 16px !important; }
        }
        /* A fingertip is a far blunter instrument than a mouse pointer, so
           give every control a real target on touch screens: 44px is the size
           both Apple and Google publish as the smallest one worth aiming at. */
        @media (pointer: coarse) {
          button, a[href] { min-height: 44px; }
        }
        /* Stops the 300ms wait for a possible second tap, which otherwise
           reads as the app being slow to answer. */
        button, a[href] { touch-action: manipulation; }
        ::-webkit-scrollbar { width: 10px; height: 10px; }
        ::-webkit-scrollbar-track { background: ${COLORS.bg}; }
        ::-webkit-scrollbar-thumb { background: ${COLORS.border}; border-radius: 5px; }
        ::-webkit-scrollbar-thumb:hover { background: ${COLORS.borderBright}; }
      `}</style>
      {children}
    </div>
  )
}
