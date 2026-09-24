import { useMemo, useState } from 'react'
import {
  RefreshCw,
  Radio,
  Square,
  Cpu,
  Activity,
  ChevronRight,
  Circle,
  Bell,
  Mic,
  Battery,
  BatteryWarning,
  User,
  Play,
  ArrowUp,
  ArrowDown,
  MousePointer,
  Hand,
  Trash2,
  MessageSquare,
  ListTodo,
  Monitor,
} from 'lucide-react'
import { useGlassDebug, GLASS_CONNECTION_EVENTS_MAX } from './useGlassDebug'
import { GlassScenePreview } from './GlassScenePreview'
import { SceneTree } from './SceneTree'
import { client } from '../../application/generated-client'
import * as glassDebug from '../../gen-clients/glass_interact/client'
import type {
  GlassCapability,
  GlassDebugState,
  GlassLifecycleEvent,
  GlassRenderFrame,
  GlassSpeechAck,
} from '../../gen-clients/system/types'
import './GlassDebugPanel.css'

function fmtTime(ts: string | undefined): string {
  if (!ts) return '—'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function fmtDateTime(ts: string | undefined): string {
  if (!ts) return '—'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleString()
}

function connectionStatus(state: GlassDebugState | null): { label: string; cls: string } {
  if (!state?.Session) return { label: 'No session', cls: 'glass-debug-badge-idle' }
  if (state.Session.Online) return { label: 'Online', cls: 'glass-debug-badge-online' }
  return { label: 'Offline', cls: 'glass-debug-badge-offline' }
}

function timelineKindLabel(kind: string): string {
  switch (kind) {
    case 'render':
      return 'render'
    case 'speak':
      return 'speak'
    case 'speak_deduped':
      return 'speak dup'
    case 'utterance':
      return 'utterance'
    case 'transcript':
      return 'transcript'
    case 'error':
      return 'error'
    default:
      return kind
  }
}

function FrameCard({ frame, online }: { frame: GlassRenderFrame | undefined; online?: boolean }) {
  return (
    <section className="glass-debug-card glass-debug-canvas-card">
      <GlassScenePreview frame={frame} online={online} />
      {frame ? (
        <div className="glass-debug-frame-meta">
          {frame.Scene ? <span className="glass-debug-chip">tick: {frame.Scene.Tick}</span> : null}
          {frame.Scene ? <span className="glass-debug-chip">elements: {frame.Scene.Elements?.length ?? 0}</span> : null}
          {frame.Layout ? <span className="glass-debug-chip">layout: {frame.Layout}</span> : null}
          {frame.Text && !frame.Scene ? <span className="glass-debug-chip glass-debug-chip-wide" title={frame.Text}>text: {frame.Text}</span> : null}
        </div>
      ) : null}
      <SceneTree frame={frame} />
    </section>
  )
}

function CapabilitiesCard({ capabilities }: { capabilities: GlassCapability[] }) {
  return (
    <section className="glass-debug-card">
      <h4 className="glass-debug-card-title">
        <Cpu size={13} /> Capabilities
        <span className="glass-debug-count">{capabilities.length}</span>
      </h4>
      {capabilities.length === 0 ? (
        <div className="glass-debug-empty">No capabilities reported</div>
      ) : (
        <ul className="glass-debug-caps">
          {capabilities.map((c, i) => (
            <li key={`${c.Name}-${i}`} className="glass-debug-cap">
              <span className="glass-debug-cap-name">{c.Name}</span>
              {c.Version ? <span className="glass-debug-chip">v{c.Version}</span> : null}
              {c.Features && c.Features.length > 0 ? (
                <span className="glass-debug-cap-features">
                  {c.Features.map(f => (
                    <span key={f} className="glass-debug-chip">{f}</span>
                  ))}
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function AudioAckCard({ ack, stats }: { ack: GlassSpeechAck | undefined; stats: GlassDebugState['Stats'] }) {
  const rows: Array<[string, string]> = useMemo(() => {
    const s = stats ?? { Utterances: 0, Transcripts: 0, Renders: 0, Speaks: 0, SpeakDeduped: 0, Errors: 0 }
    return [
      ['Utterances', String(s.Utterances ?? 0)],
      ['Transcripts', String(s.Transcripts ?? 0)],
      ['Renders', String(s.Renders ?? 0)],
      ['Speaks', String(s.Speaks ?? 0)],
      ['Speak deduped', String(s.SpeakDeduped ?? 0)],
      ['Errors', String(s.Errors ?? 0)],
    ]
  }, [stats])

  return (
    <section className="glass-debug-card">
      <h4 className="glass-debug-card-title">
        <Activity size={13} /> Audio & ACK
      </h4>
      {!ack ? (
        <div className="glass-debug-empty">No audio in flight</div>
      ) : (
        <div className="glass-debug-ack">
          <div className="glass-debug-ack-row">
            <span className="glass-debug-ack-label">Utterance</span>
            <span className="glass-debug-ack-value">{ack.UtteranceId}</span>
          </div>
          <div className="glass-debug-ack-row">
            <span className="glass-debug-ack-label">Contiguous seq</span>
            <span className="glass-debug-ack-value">{ack.HighestContiguousSeq}</span>
          </div>
          <div className="glass-debug-ack-row">
            <span className="glass-debug-ack-label">End received</span>
            <span className="glass-debug-ack-value">{ack.EndReceived ? 'yes' : 'no'}</span>
          </div>
          <div className="glass-debug-ack-row">
            <span className="glass-debug-ack-label">Accepted</span>
            <span className={`glass-debug-ack-value ${ack.Accepted ? '' : 'glass-debug-ack-rejected'}`}>{ack.Accepted ? 'yes' : 'no'}</span>
          </div>
          {ack.Reason ? (
            <div className="glass-debug-ack-row">
              <span className="glass-debug-ack-label">Reason</span>
              <span className="glass-debug-ack-value">{ack.Reason}</span>
            </div>
          ) : null}
        </div>
      )}
      <div className="glass-debug-stats">
        {rows.map(([label, value]) => (
          <div key={label} className="glass-debug-stat">
            <span className="glass-debug-stat-value">{value}</span>
            <span className="glass-debug-stat-label">{label}</span>
          </div>
        ))}
      </div>
    </section>
  )
}

function ConnectionCard({ state, connectionEvents }: { state: GlassDebugState | null; connectionEvents: GlassLifecycleEvent[] }) {
  const session = state?.Session
  return (
    <section className="glass-debug-card">
      <h4 className="glass-debug-card-title">
        <Radio size={13} /> Session
        <span className={`glass-debug-badge ${connectionStatus(state).cls}`}>{connectionStatus(state).label}</span>
      </h4>
      {!session ? (
        <div className="glass-debug-empty">No active Glass session</div>
      ) : (
        <div className="glass-debug-kv">
          <div className="glass-debug-kv-row">
            <span className="glass-debug-kv-key">Session ID</span>
            <span className="glass-debug-kv-value">{session.SessionId}</span>
          </div>
          <div className="glass-debug-kv-row">
            <span className="glass-debug-kv-key">Device ID</span>
            <span className="glass-debug-kv-value">{session.DeviceId}</span>
          </div>
          <div className="glass-debug-kv-row">
            <span className="glass-debug-kv-key">Generation</span>
            <span className="glass-debug-kv-value">{session.Generation}</span>
          </div>
          <div className="glass-debug-kv-row">
            <span className="glass-debug-kv-key">Last seen</span>
            <span className="glass-debug-kv-value">{fmtDateTime(session.LastSeen)}</span>
          </div>
          <div className="glass-debug-kv-row">
            <span className="glass-debug-kv-key">Offline at</span>
            <span className="glass-debug-kv-value">{fmtDateTime(session.OfflineAt)}</span>
          </div>
        </div>
      )}
      <div className="glass-debug-conn-events">
        <span className="glass-debug-conn-events-label">Connection events</span>
        {connectionEvents.length === 0 ? (
          <div className="glass-debug-empty">None observed this session</div>
        ) : (
          <ul className="glass-debug-conn-list">
            {connectionEvents.map((ev, i) => (
              <li key={`${ev.Kind}-${ev.Timestamp}-${i}`} className="glass-debug-conn-item">
                <span className={`glass-debug-conn-kind glass-debug-conn-kind-${ev.Kind}`}>{ev.Kind}</span>
                <span className="glass-debug-conn-time">{fmtTime(ev.Timestamp)}</span>
                {ev.Reason ? <span className="glass-debug-conn-reason">{ev.Reason}</span> : null}
              </li>
            ))}
          </ul>
        )}
        {connectionEvents.length >= GLASS_CONNECTION_EVENTS_MAX ? (
          <span className="glass-debug-conn-note">showing last {GLASS_CONNECTION_EVENTS_MAX}</span>
        ) : null}
      </div>
    </section>
  )
}

function TimelineCard({ state }: { state: GlassDebugState | null }) {
  const timeline = state?.Timeline ?? []
  return (
    <section className="glass-debug-card">
      <h4 className="glass-debug-card-title">
        <Square size={13} /> Timeline
        <span className="glass-debug-count">{timeline.length}</span>
      </h4>
      {timeline.length === 0 ? (
        <div className="glass-debug-empty">No activity recorded</div>
      ) : (
        <ul className="glass-debug-timeline">
          {timeline.map((e, i) => (
            <li key={`${e.Kind}-${e.Timestamp}-${i}`} className="glass-debug-timeline-item">
              <span className={`glass-debug-timeline-kind glass-debug-timeline-kind-${e.Kind}`}>{timelineKindLabel(e.Kind)}</span>
              <span className="glass-debug-timeline-detail">{e.Detail ?? ''}</span>
              <span className="glass-debug-timeline-time">{fmtTime(e.Timestamp)}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

const SAMPLE_NOTIFICATION = {
  Title: 'coder',
  Body: '代码重构已完成\n3 个测试通过，git 已提交\n\n涉及文件：\n· coordinator_route.go\n· scene.go',
  Source: 'coder',
  TimeAgo: '3m前',
}

const SAMPLE_ASKUSER = {
  Context: '测试失败',
  Title: '发现 3 个测试失败，如何处理？',
  Options: ['自动修复', '重新运行测试', '忽略并继续', '查看失败详情'],
}

function SimulationCard({ onResult }: { onResult: (msg: string) => void }) {
  const [askSelected, setAskSelected] = useState(0)
  const [working, setWorking] = useState(false)

  async function simulate(command: string, extra: Partial<Parameters<typeof glassDebug.debugSimulate>[1]> = {}) {
    setWorking(true)
    try {
      const resp = await glassDebug.debugSimulate(client, { Command: command, ...extra } as Parameters<typeof glassDebug.debugSimulate>[1])
      onResult(`${command}: ${resp.Applied ? 'ok' : 'failed'} ${resp.Detail ?? ''}`)
    } catch (err) {
      onResult(`${command}: error ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setWorking(false)
    }
  }

  return (
    <section className="glass-debug-card glass-debug-simulation">
      <h4 className="glass-debug-card-title">
        <Monitor size={13} /> Simulation
      </h4>

      <div className="glass-debug-sim-section">
        <span className="glass-debug-sim-label">UI states</span>
        <div className="glass-debug-sim-row">
          <button className="glass-debug-sim-btn" onClick={() => simulate('render_idle')} disabled={working}>
            <Play size={12} /> Idle
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('render_notification', SAMPLE_NOTIFICATION)} disabled={working}>
            <Bell size={12} /> Notify
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('render_askuser', { ...SAMPLE_ASKUSER, SelectedIdx: askSelected })} disabled={working}>
            <ListTodo size={12} /> AskUser
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('render_transcript', { Body: '帮我检查 coordinator 路由的测试是否覆盖了 unicode 昵称', SelectedIdx: 18 })} disabled={working}>
            <Mic size={12} /> Transcript
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('clear')} disabled={working}>
            <Trash2 size={12} /> Clear
          </button>
        </div>
        <div className="glass-debug-sim-row">
          <span className="glass-debug-sim-hint">AskUser selection</span>
          <button className="glass-debug-sim-btn small" onClick={() => setAskSelected(v => Math.max(0, v - 1))} disabled={working}>
            <ArrowUp size={12} />
          </button>
          <span className="glass-debug-sim-value">{askSelected}</span>
          <button className="glass-debug-sim-btn small" onClick={() => setAskSelected(v => Math.min(SAMPLE_ASKUSER.Options.length - 1, v + 1))} disabled={working}>
            <ArrowDown size={12} />
          </button>
        </div>
      </div>

      <div className="glass-debug-sim-section">
        <span className="glass-debug-sim-label">System events</span>
        <div className="glass-debug-sim-row">
          <button className="glass-debug-sim-btn" onClick={() => simulate('speak', { Body: '代码重构已完成' })} disabled={working}>
            <Mic size={12} /> Speak
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('enqueue', { Title: 'coder 完成任务', Priority: 'normal' })} disabled={working}>
            <MessageSquare size={12} /> Enqueue
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('battery', { BatteryLevel: 87 })} disabled={working}>
            <Battery size={12} /> 87%
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('battery', { BatteryLevel: 15 })} disabled={working}>
            <BatteryWarning size={12} /> 15%
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('agent_running', { Source: 'coder' })} disabled={working}>
            <User size={12} /> Agent run
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('agent_idle', { Source: 'coder' })} disabled={working}>
            <Square size={12} /> Agent idle
          </button>
        </div>
      </div>

      <div className="glass-debug-sim-section">
        <span className="glass-debug-sim-label">Glasses input</span>
        <div className="glass-debug-sim-row">
          <button className="glass-debug-sim-btn" onClick={() => simulate('button_tap')} disabled={working}>
            <MousePointer size={12} /> Tap
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('button_hold')} disabled={working}>
            <Hand size={12} /> Hold
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('swipe_up')} disabled={working}>
            <ArrowUp size={12} /> Swipe up
          </button>
          <button className="glass-debug-sim-btn" onClick={() => simulate('swipe_down')} disabled={working}>
            <ArrowDown size={12} /> Swipe down
          </button>
        </div>
      </div>
    </section>
  )
}

export function GlassDebugPanel() {
  const { state, loading, error, lastUpdated, autoRefresh, setAutoRefresh, refresh, connectionEvents } = useGlassDebug(true)
  const status = connectionStatus(state)
  const capabilities = state?.Capabilities ?? []
  const session = state?.Session
  const [simMsg, setSimMsg] = useState<string | null>(null)

  return (
    <div className="glass-debug">
      <div className="glass-debug-toolbar">
        <div className="glass-debug-title">
          <Activity size={14} />
          Glass Debug
          <span className={`glass-debug-badge ${status.cls}`}>
            <Circle size={8} fill="currentColor" />
            {status.label}
          </span>
          {session ? (
            <span className="glass-debug-subtitle">gen {session.Generation} · {session.DeviceId}</span>
          ) : null}
        </div>
        <div className="glass-debug-actions">
          <label className="glass-debug-autorefresh" title="Poll the debug snapshot over /ws">
            <input type="checkbox" checked={autoRefresh} onChange={e => setAutoRefresh(e.target.checked)} />
            auto
          </label>
          <button className="glass-debug-refresh" onClick={refresh} title="Refresh debug snapshot" disabled={loading}>
            <RefreshCw size={13} className={loading ? 'glass-debug-spin' : ''} />
          </button>
          {lastUpdated ? <span className="glass-debug-updated">{fmtTime(new Date(lastUpdated).toISOString())}</span> : null}
        </div>
      </div>

      {error ? (
        <div className="glass-debug-error">
          <ChevronRight size={12} />
          {error}
        </div>
      ) : null}

      {simMsg ? (
        <div className="glass-debug-sim-msg">
          <ChevronRight size={12} />
          {simMsg}
          <button className="glass-debug-sim-msg-close" onClick={() => setSimMsg(null)}>×</button>
        </div>
      ) : null}

      <div className="glass-debug-grid">
        <ConnectionCard state={state} connectionEvents={connectionEvents} />
        <FrameCard frame={state?.CurrentFrame} online={state?.Session?.Online} />
        <SimulationCard onResult={setSimMsg} />
        <CapabilitiesCard capabilities={capabilities} />
        <AudioAckCard ack={state?.AudioAck} stats={state?.Stats ?? { Utterances: 0, Transcripts: 0, Renders: 0, Speaks: 0, SpeakDeduped: 0, Errors: 0 }} />
        <TimelineCard state={state} />
      </div>
    </div>
  )
}
