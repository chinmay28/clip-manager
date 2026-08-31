import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { api, clipURL, playURL } from './api'
import { COLORS, FONT, formatBytes } from './theme'
import { Brand, DevMark } from './components/Brand'

/* The app is two answers on one page: what the cameras have recorded, and how
   much of the disk it is taking. Everything else — playing a clip, drawing a
   quota, running a cleanup — hangs off one of those two. */
export function App() {
  const [clips, setClips] = useState(null)
  const [storage, setStorage] = useState(null)
  const [sources, setSources] = useState(null)
  const [error, setError] = useState(null)
  const [playing, setPlaying] = useState(null)

  const refresh = useCallback(async () => {
    try {
      const [c, s, src] = await Promise.all([api.clips(), api.storage(), api.sources()])
      setClips(c.clips || [])
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

        {sources && storage && (
          <SourcesPanel sources={sources} usage={storage.usage} onChanged={refresh} />
        )}
        {storage && <StoragePanel storage={storage} onChanged={refresh} />}
        {clips && (
          <ClipList
            clips={clips}
            onPlay={setPlaying}
            // Naming the source on every row only earns its ink once there
            // is more than one place a clip could have come from.
            showSource={(sources || []).length > 1}
          />
        )}
        {!clips && !error && (
          <p style={{ color: COLORS.textMuted }}>Loading…</p>
        )}
      </main>

      {playing && <Player clip={playing} onClose={() => setPlaying(null)} />}
    </Shell>
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

function ClipList({ clips, onPlay, showSource }) {
  const [camera, setCamera] = useState(null)

  const cameras = useMemo(
    () => [...new Set(clips.map((c) => c.camera))].sort(),
    [clips],
  )
  // Newest first for reading; the API's oldest-first order belongs to
  // enforcement, not to people.
  const shown = useMemo(() => {
    const filtered = camera == null ? clips : clips.filter((c) => c.camera === camera)
    return [...filtered].reverse()
  }, [clips, camera])

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
      {cameras.length > 1 && (
        <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap', marginBottom: '12px' }}>
          <Chip active={camera == null} onClick={() => setCamera(null)}>All</Chip>
          {cameras.map((name) => (
            <Chip key={name} active={camera === name} onClick={() => setCamera(name)}>
              {name || 'no camera'}
            </Chip>
          ))}
        </div>
      )}

      <div style={{ borderRadius: '10px', border: `1px solid ${COLORS.border}`, overflow: 'hidden' }}>
        {shown.map((clip) => (
          <ClipRow
            key={`${clip.source}:${clip.path}`}
            clip={clip}
            onPlay={onPlay}
            showSource={showSource}
          />
        ))}
      </div>
    </section>
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

function ClipRow({ clip, onPlay, showSource }) {
  const when = new Date(clip.mod_time)
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
        }}>{clip.name}</div>
        <div style={{ color: COLORS.textMuted, fontSize: '12px' }}>
          {clip.camera && <>{clip.camera} · </>}
          {showSource && <span title={clip.source}>{baseName(clip.source)} · </span>}
          {formatBytes(clip.size)} · {when.toLocaleString()}
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
   with range support, so scrubbing works; a remuxed one (.dav) is a live
   stream out of ffmpeg with no known length, so it plays from the start —
   which for a clip a few seconds long is the whole clip.

   Failure here is ordinary — a codec this browser lacks, a truncated file —
   and it must land as a sentence with a way out, not a black rectangle. */
function Player({ clip, onClose }) {
  const [failed, setFailed] = useState(false)

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
            This clip's video stream is one this browser cannot decode —
            repackaging changes the container, never the codec. Download it
            and play it in VLC.
          </div>
        ) : (
          <video
            src={clip.playable ? clipURL(clip) : playURL(clip)}
            controls
            autoPlay
            onError={() => setFailed(true)}
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
