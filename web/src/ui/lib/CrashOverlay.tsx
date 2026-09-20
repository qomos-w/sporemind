import { useEffect, useState, type CSSProperties, type ReactNode } from 'react'
import { isWailsAvailable, onWailsEvent } from '../../application/wails-runtime'
import * as desktopApp from '../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import type { BootCrashReport, CrashRecordInfo } from '../../bindings/github.com/qomos-w/sporemind/pkg/desktop/models'
import { useBrowserOverlay } from '../ai/browserOverlay'

interface CrashData {
  error: string
  time: string
  stack: string
  fullStack?: string
}

const kindLabel: Record<string, string> = {
  panic: 'Go panic',
  fatal: 'Fatal error',
  native: 'Native crash',
  'abnormal-exit': 'Abnormal exit',
}

/**
 * CrashOverlay renders a live crash (delivered via the sporemind:crash event
 * right before the process dies) AND a previous-session crash notice fetched
 * once at startup via BootCrashReport. The startup notice is acknowledged
 * through AckCrashRecords so it is not shown again. Mounted inside
 * BrowserOverlayManager so any HTML raised over the embedded native browser
 * windows is driven through useBrowserOverlay.
 */
export function CrashOverlay() {
  const [liveCrash, setLiveCrash] = useState<CrashData | null>(null)
  const [bootReport, setBootReport] = useState<BootCrashReport | null>(null)

  useEffect(() => {
    const cancel = onWailsEvent('sporemind:crash', (...args: unknown[]) => {
      const data = args[0] as CrashData
      if (data?.error) {
        setLiveCrash(data)
      }
    })
    return () => { cancel?.() }
  }, [])

  useEffect(() => {
    if (!isWailsAvailable() || bootReport !== null) return
    desktopApp
      .BootCrashReport()
      .then((r) => {
        if (r && (r.abnormalExit || (r.records?.length ?? 0) > 0)) {
          setBootReport(r)
        }
      })
      .catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const open = liveCrash !== null || bootReport !== null
  useBrowserOverlay(open)

  const dismissBootCrash = () => {
    const ids = (bootReport?.records ?? []).map((r) => r.id)
    if (ids.length > 0) {
      desktopApp.AckCrashRecords(ids).catch(() => {})
    }
    setBootReport(null)
  }

  if (liveCrash) {
    return <LiveCrashView crash={liveCrash} />
  }
  if (bootReport) {
    return <BootCrashView report={bootReport} onDismiss={dismissBootCrash} />
  }
  return null
}

function LiveCrashView({ crash }: { crash: CrashData }) {
  return (
    <CrashShell title="Application Crash" subtitle="sporemind encountered a fatal error and must close. A crash report has been saved to the logs directory." accent="#e94560">
      <div style={styles.code}>{crash.error}</div>
      {crash.stack && <StackTraceBlock label="Stack Trace" text={crash.stack} />}
      <p style={styles.time}>{crash.time}</p>
    </CrashShell>
  )
}

function BootCrashView({ report, onDismiss }: { report: BootCrashReport; onDismiss: () => void }) {
  return (
    <CrashShell
      title="Previous session crashed"
      subtitle={
        report.abnormalExit
          ? 'sporemind did not shut down cleanly last time — likely a crash, forced termination, or power loss.'
          : 'A crash report was captured last session.'
      }
      accent="#e94560"
      footer={<button style={styles.button} onClick={onDismiss}>Dismiss</button>}
    >
      {(report.records ?? []).map((r) => (
        <RecordBlock key={r.id} record={r} />
      ))}
      {report.errorsTail?.length ? <StackTraceBlock label="Error log (tail)" text={report.errorsTail.join('\n')} /> : null}
      {report.logTail?.length ? <StackTraceBlock label="Application log (tail)" text={report.logTail.join('\n')} /> : null}
      {report.previousSession && (
        <p style={styles.time}>Last session started: {report.previousSession.startedAt}{report.previousSession.version ? ` · ${report.previousSession.version}` : ''}</p>
      )}
    </CrashShell>
  )
}

function RecordBlock({ record }: { record: CrashRecordInfo }) {
  return (
    <div style={styles.record}>
      <div style={styles.recordHead}>
        <span style={styles.kind}>{kindLabel[record.kind] ?? record.kind}</span>
        {record.pid ? <span style={styles.pid}>pid {record.pid}</span> : null}
        {record.time ? <span style={styles.timeInline}>{record.time}</span> : null}
      </div>
      <div style={styles.summary}>{record.summary}</div>
      {record.dumpFile ? <div style={styles.dump}>dump: {record.dumpFile}</div> : null}
      {record.detail ? <StackTraceBlock label="Detail" text={record.detail} /> : null}
    </div>
  )
}

function StackTraceBlock({ label, text }: { label: string; text: string }) {
  return (
    <details style={styles.details}>
      <summary style={styles.summary2}>{label}</summary>
      <pre style={styles.pre}>{text}</pre>
    </details>
  )
}

const outer: CSSProperties = {
  position: 'fixed',
  inset: 0,
  zIndex: 99999,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: 'rgba(0, 0, 0, 0.75)',
  fontFamily: 'system-ui, -apple-system, sans-serif',
}

const styles: Record<string, CSSProperties> = {
  card: { maxWidth: 640, width: '90%', maxHeight: '80vh', overflow: 'auto', background: '#1a1a2e', border: '1px solid #e94560', borderRadius: 12, padding: 24, color: '#eee', boxShadow: '0 8px 32px rgba(0,0,0,0.5)' },
  title: { color: '#e94560', margin: '0 0 8px 0', fontSize: 20 },
  subtitle: { color: '#aaa', fontSize: 13, margin: '0 0 16px 0' },
  code: { background: '#0d0d1a', borderRadius: 8, padding: 12, marginBottom: 16, fontFamily: 'monospace', fontSize: 12, color: '#e94560', whiteSpace: 'pre-wrap', wordBreak: 'break-all' },
  details: { marginBottom: 8 },
  summary2: { cursor: 'pointer', color: '#aaa', fontSize: 13 },
  pre: { background: '#0d0d1a', borderRadius: 8, padding: 12, marginTop: 8, fontFamily: 'monospace', fontSize: 11, color: '#888', whiteSpace: 'pre-wrap', wordBreak: 'break-all', overflow: 'auto', maxHeight: 300 },
  time: { color: '#666', fontSize: 11, margin: '12px 0 0 0' },
  record: { background: '#0d0d1a', borderRadius: 8, padding: 12, marginBottom: 12 },
  recordHead: { display: 'flex', gap: 8, alignItems: 'center', marginBottom: 6, fontSize: 12 },
  kind: { color: '#e94560', fontWeight: 600 },
  pid: { color: '#888' },
  timeInline: { color: '#666', marginLeft: 'auto' },
  summary: { fontFamily: 'monospace', fontSize: 12, color: '#ddd', whiteSpace: 'pre-wrap', wordBreak: 'break-word' },
  dump: { color: '#666', fontSize: 11, marginTop: 6 },
  button: { marginTop: 4, padding: '8px 18px', background: '#e94560', color: '#fff', border: 'none', borderRadius: 8, cursor: 'pointer', fontSize: 13 },
  footer: { marginTop: 16, textAlign: 'right' },
}

function CrashShell({
  title,
  subtitle,
  accent,
  footer,
  children,
}: {
  title: string
  subtitle: string
  accent: string
  footer?: ReactNode
  children: ReactNode
}) {
  return (
    <div style={outer}>
      <div style={{ ...styles.card, borderColor: accent }}>
        <h2 style={{ ...styles.title, color: accent }}>{title}</h2>
        <p style={styles.subtitle}>{subtitle}</p>
        {children}
        {footer ? <div style={styles.footer}>{footer}</div> : null}
      </div>
    </div>
  )
}
