import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react'
import * as api from '../api/client'
import type {
  BookSnapshot,
  ChapterDetail,
  CoCreateMessage,
  ModelConfig,
  ModelConfiguration,
  ProviderSnapshot,
} from '../api/types'
import {
  useConnectionStatus,
  useEventLog,
  useImportEvent,
  useSnapshot,
  useStreamBuffer,
} from '../state/sseHooks'

type ModalName = 'settings' | 'import' | 'export' | 'cocreate' | null

export function Dashboard() {
  const status = useConnectionStatus()
  const snapshot = useSnapshot()
  const events = useEventLog()
  const stream = useStreamBuffer()
  const importEvent = useImportEvent()
  const [book, setBook] = useState<BookSnapshot | null>(null)
  const [chapter, setChapter] = useState<ChapterDetail | null>(null)
  const [selectedChapter, setSelectedChapter] = useState(0)
  const [view, setView] = useState<'studio' | 'manuscript'>('studio')
  const [modal, setModal] = useState<ModalName>(null)
  const [coCreateStage, setCoCreateStage] = useState(false)
  const [prompt, setPrompt] = useState('')
  const [command, setCommand] = useState('')
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)

  const refreshBook = useCallback(() => api.getBook().then(setBook).catch(() => undefined), [])

  useEffect(() => {
    void refreshBook()
  }, [refreshBook, snapshot?.CompletedCount, snapshot?.TotalChapters, snapshot?.Phase])

  useEffect(() => {
    if (!selectedChapter && book?.chapters.length) {
      setSelectedChapter(book.chapters.find((item) => item.status === 'completed')?.chapter || 1)
    }
  }, [book, selectedChapter])

  useEffect(() => {
    if (!selectedChapter) return
    api.getChapter(selectedChapter).then(setChapter).catch((err) => setNotice(messageOf(err)))
  }, [selectedChapter, snapshot?.CompletedCount])

  async function run(action: () => Promise<unknown>, success?: string) {
    setNotice(null)
    setBusy(true)
    try {
      await action()
      if (success) setNotice(success)
    } catch (err) {
      setNotice(messageOf(err))
    } finally {
      setBusy(false)
    }
  }

  function openChapter(value: number) {
    setSelectedChapter(value)
    setView('manuscript')
  }

  async function openStageCoCreate() {
    setNotice(null)
    try {
      await api.pauseForCoCreate()
      setCoCreateStage(true)
      setModal('cocreate')
    } catch (err) {
      setNotice(messageOf(err))
    }
  }

  const progress = snapshot?.TotalChapters
    ? Math.min(100, Math.round((snapshot.CompletedCount / snapshot.TotalChapters) * 100))
    : 0

  return (
    <div className="app-shell">
      <header className="topbar">
        <button className="brand" onClick={() => setView('studio')} aria-label="Open studio">
          <span className="brand-mark">A</span>
          <span><strong>AINOVEL</strong><small>LOCAL STUDIO</small></span>
        </button>
        <div className="topbar-center">
          <span className={`connection ${status}`}><i />{status === 'open' ? 'Local engine online' : status}</span>
          {snapshot?.BookTitle && <span className="book-title">{snapshot.BookTitle}</span>}
        </div>
        <div className="top-actions">
          <button className="button ghost" onClick={() => setModal('import')}>Import</button>
          <button className="button ghost" onClick={() => setModal('export')} disabled={!snapshot?.CompletedCount}>Export</button>
          <button className="icon-button" onClick={() => setModal('settings')} aria-label="Model settings">⚙</button>
        </div>
      </header>

      <div className="workspace">
        <aside className="left-rail">
          <nav className="main-nav">
            <button className={view === 'studio' ? 'active' : ''} onClick={() => setView('studio')}><span>✦</span>Studio</button>
            <button className={view === 'manuscript' ? 'active' : ''} onClick={() => setView('manuscript')}><span>▤</span>Manuscript</button>
          </nav>

          <section className="progress-card">
            <div className="eyebrow">BOOK PROGRESS</div>
            <div className="progress-value"><strong>{progress}%</strong><span>{snapshot?.CompletedCount || 0}/{snapshot?.TotalChapters || 0} chapters</span></div>
            <div className="progress-track"><i style={{ width: `${progress}%` }} /></div>
            <div className="stat-pair"><span>{formatNumber(snapshot?.TotalWordCount || 0)} words</span><span>{snapshot?.Phase || 'not started'}</span></div>
          </section>

          <div className="rail-heading"><span>OUTLINE</span><button onClick={() => void refreshBook()} aria-label="Refresh outline">↻</button></div>
          <div className="chapter-list">
            {book?.chapters.length ? book.chapters.map((item) => (
              <button key={item.chapter} className={selectedChapter === item.chapter ? 'selected' : ''} onClick={() => openChapter(item.chapter)}>
                <span className={`chapter-dot ${item.status}`} />
                <span className="chapter-copy"><b>Chapter {item.chapter}</b><small>{item.title || item.coreEvent || 'Planned chapter'}</small></span>
                {item.wordCount > 0 && <em>{compactNumber(item.wordCount)}</em>}
              </button>
            )) : <p className="empty-rail">Your outline will appear here once planning begins.</p>}
          </div>
        </aside>

        <main className="main-stage">
          {notice && <div className="notice" role="status"><span>{notice}</span><button onClick={() => setNotice(null)}>×</button></div>}
          {view === 'studio' ? (
            <StudioView
              snapshot={snapshot}
              stream={stream}
              prompt={prompt}
              setPrompt={setPrompt}
              command={command}
              setCommand={setCommand}
              busy={busy}
              run={run}
              onColdCoCreate={() => { setCoCreateStage(false); setModal('cocreate') }}
              onStageCoCreate={() => void openStageCoCreate()}
            />
          ) : (
            <ManuscriptView book={book} chapter={chapter} selected={selectedChapter} onSelect={openChapter} />
          )}
        </main>

        <aside className="right-rail">
          <section className="engine-card">
            <div className="section-title"><span>ENGINE</span><StatusBadge value={snapshot?.RuntimeState || 'idle'} /></div>
            <dl>
              <div><dt>Phase</dt><dd>{snapshot?.Phase || '—'}</dd></div>
              <div><dt>Flow</dt><dd>{snapshot?.Flow || '—'}</dd></div>
              <div><dt>Model</dt><dd title={snapshot?.ModelName}>{snapshot?.ModelName || '—'}</dd></div>
              <div><dt>Cost</dt><dd>${(snapshot?.TotalCostUSD || 0).toFixed(3)}</dd></div>
            </dl>
          </section>

          <section className="agents-panel">
            <div className="section-title"><span>AGENTS</span><small>{snapshot?.Agents.length || 0} active roles</small></div>
            <div className="agent-list">
              {snapshot?.Agents.length ? snapshot.Agents.map((agent) => (
                <div className="agent-row" key={agent.Name}>
                  <span className="agent-avatar">{agent.Name.slice(0, 1).toUpperCase()}</span>
                  <span><b>{agent.Name}</b><small>{agent.Summary || agent.State}</small></span>
                  <i className={agent.State.toLowerCase()} />
                </div>
              )) : <p className="empty-small">Agents wake when a writing task starts.</p>}
            </div>
          </section>

          <section className="activity-panel">
            <div className="section-title"><span>ACTIVITY</span><small>live</small></div>
            <div className="activity-list">
              {events.slice(-18).reverse().map((event, index) => (
                <div className={`activity-row ${event.Level}`} key={event.ID || `${event.Time}-${index}`}>
                  <time>{formatTime(event.Time)}</time>
                  <span><b>{event.Category}</b>{event.Summary}</span>
                </div>
              ))}
              {!events.length && <p className="empty-small">Runtime events will stream here.</p>}
            </div>
          </section>
        </aside>
      </div>

      {modal === 'settings' && <SettingsModal onClose={() => setModal(null)} onNotice={setNotice} />}
      {modal === 'import' && <ImportModal event={importEvent} onClose={() => setModal(null)} onImported={refreshBook} />}
      {modal === 'export' && <ExportModal total={snapshot?.TotalChapters || 0} onClose={() => setModal(null)} />}
      {modal === 'cocreate' && (
        <CoCreateModal
          stage={coCreateStage}
          onClose={() => setModal(null)}
          onStarted={() => { setModal(null); setView('studio') }}
        />
      )}
    </div>
  )
}

function StudioView({ snapshot, stream, prompt, setPrompt, command, setCommand, busy, run, onColdCoCreate, onStageCoCreate }: any) {
  const hasBook = Boolean(snapshot?.BookTitle || snapshot?.Phase || snapshot?.IsRunning)
  const running = Boolean(snapshot?.IsRunning)
  const completed = snapshot?.RuntimeState === 'completed' || snapshot?.Phase === 'complete'

  if (!hasBook) {
    return (
      <div className="start-screen">
        <div className="start-kicker">YOUR STORY STARTS HERE</div>
        <h1>Turn an idea into a<br /><em>finished novel.</em></h1>
        <p>Describe the story you want, or shape it through a guided conversation with your co-creator.</p>
        <form className="idea-box" onSubmit={(event) => { event.preventDefault(); void run(() => api.start(prompt)) }}>
          <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder="A near-future mystery about a memory archivist who discovers one of her own memories has been forged…" required />
          <div><span>{prompt.length} characters</span><button className="button primary" disabled={busy || !prompt.trim()}>Start writing <b>→</b></button></div>
        </form>
        <button className="cocreate-callout" onClick={onColdCoCreate}><span>✦</span><span><b>Not sure where to begin?</b><small>Build the premise together with the AI co-creator.</small></span><em>Open co-create →</em></button>
      </div>
    )
  }

  return (
    <div className="studio-view">
      <div className="stage-heading">
        <div><span className="eyebrow">CURRENT WORKSPACE</span><h1>{snapshot.BookTitle || 'Planning your story'}</h1><p>{snapshot.StatusLabel || 'The writing engine is ready.'}</p></div>
        <button className="button accent" onClick={onStageCoCreate}>✦ Plan together</button>
      </div>

      <div className="now-card">
        <div className="now-index">{String(snapshot.InProgressChapter || snapshot.CurrentChapter || 1).padStart(2, '0')}</div>
        <div><span className="eyebrow">{running ? 'WRITING NOW' : 'NEXT CHAPTER'}</span><h2>{snapshot.Flow || snapshot.Phase}</h2><p>{snapshot.LastCommitSummary || snapshot.RecoveryLabel || 'Ready for your direction.'}</p></div>
        {running ? <button className="button danger" onClick={() => void run(() => api.abort(), 'Writing paused.')}>Pause</button> : (
          <button className="button primary" onClick={() => void run(() => api.resume())} disabled={busy || completed}>Resume</button>
        )}
      </div>

      <section className="stream-panel">
        <div className="panel-heading"><span><i className={running ? 'pulse' : ''} />LIVE MODEL OUTPUT</span><small>{stream.length ? `${formatNumber(stream.length)} chars` : 'waiting'}</small></div>
        <div className="stream-copy">{stream || <span className="stream-empty">Model reasoning and prose will appear here while an agent is working.</span>}</div>
      </section>

      <form className="command-box" onSubmit={(event) => {
        event.preventDefault()
        const value = command.trim()
        if (!value) return
        void run(() => running ? api.steer(value) : api.continueIdle(value), running ? 'Direction queued.' : 'Writing resumed.')
        setCommand('')
      }}>
        <textarea value={command} onChange={(event) => setCommand(event.target.value)} placeholder={running ? 'Steer the current work without stopping it…' : 'Give the engine a new direction or ask it to continue…'} />
        <div>
          <div className="mode-switch">
            <button type="button" className={snapshot.AdvanceMode === 'auto' ? 'active' : ''} onClick={() => void run(() => api.setReviewMode('auto'))}>Auto</button>
            <button type="button" className={snapshot.AdvanceMode !== 'auto' ? 'active' : ''} onClick={() => void run(() => api.setReviewMode('review'))}>Review each</button>
          </div>
          <div className="command-actions">
            {snapshot.HasAdvanceHold && <button type="button" className="button ghost" onClick={() => void run(() => api.advanceNext())}>Approve next</button>}
            {completed && <button type="button" className="button ghost" onClick={() => void run(() => api.reopen(command))}>Reopen book</button>}
            <button className="button primary" disabled={!command.trim() || busy}>{running ? 'Send direction' : 'Continue'} <b>→</b></button>
          </div>
        </div>
      </form>
    </div>
  )
}

function ManuscriptView({ book, chapter, selected, onSelect }: { book: BookSnapshot | null; chapter: ChapterDetail | null; selected: number; onSelect: (n: number) => void }) {
  if (!book?.chapters.length) return <div className="empty-page"><span>▤</span><h2>No manuscript yet</h2><p>Start a novel or import an existing manuscript to browse chapters here.</p></div>
  const active = selected || book.chapters.find((item) => item.status === 'completed')?.chapter || 1
  const item = book.chapters.find((entry) => entry.chapter === active)
  return (
    <div className="reader-view">
      <header><span className="eyebrow">MANUSCRIPT · CHAPTER {active}</span><h1>{item?.title || `Chapter ${active}`}</h1><p>{item?.summary || item?.coreEvent}</p></header>
      <article>{chapter?.content ? chapter.content.split(/\n\s*\n/).map((paragraph, index) => <p key={index}>{paragraph}</p>) : <div className="empty-page compact"><h2>Chapter not written yet</h2><p>The outline is ready, but no draft has been saved.</p></div>}</article>
      <footer>
        <button disabled={active <= 1} onClick={() => onSelect(active - 1)}>← Previous</button>
        <span>{item?.wordCount ? `${formatNumber(item.wordCount)} words` : chapter?.status}</span>
        <button disabled={active >= book.chapters.length} onClick={() => onSelect(active + 1)}>Next →</button>
      </footer>
    </div>
  )
}

function SettingsModal({ onClose, onNotice }: { onClose: () => void; onNotice: (value: string) => void }) {
  const [config, setConfig] = useState<ModelConfiguration | null>(null)
  const [selected, setSelected] = useState('')
  const [provider, setProvider] = useState<ProviderSnapshot | null>(null)
  const [models, setModels] = useState<ModelConfig[]>([])
  const [apiKey, setApiKey] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => { api.getModelConfiguration().then(setConfig).catch((err) => setError(messageOf(err))) }, [])
  useEffect(() => {
    if (!config) return
    const name = selected || config.DefaultProvider
    const found = config.Providers.find((item) => item.Name === name)
    if (!found) return
    setSelected(found?.Name || '')
    setProvider(found ? { ...found } : null)
    setModels(found?.Models.map((model) => ({ ...model })) || [])
    setApiKey('')
  }, [config, selected])

  const draft = () => ({
    provider: provider?.Name || '', type: provider?.Type || '', api: provider?.API || '', baseURL: provider?.BaseURL || '', models,
    apiKeyAction: (apiKey ? 'replace' : 'keep') as 'replace' | 'keep', apiKey, testModel: models[0]?.name || '',
  })

  async function act(task: () => Promise<unknown>, success: string) {
    setSaving(true); setError(null)
    try { await task(); onNotice(success); setConfig(await api.getModelConfiguration()) } catch (err) { setError(messageOf(err)) } finally { setSaving(false) }
  }

  return (
    <Modal title="Model & provider settings" subtitle="Credentials stay on this machine and are never returned to the browser." onClose={onClose} wide>
      {!config || !provider ? <p>Loading configuration…</p> : <div className="settings-grid">
        <aside>
          {config.Providers.map((item) => <button key={item.Name} className={selected === item.Name ? 'active' : ''} onClick={() => setSelected(item.Name)}><b>{item.Name}</b><small>{item.Models.length} models · {item.HasAPIKey ? item.APIKeyHint : 'no key'}</small></button>)}
          <button className="add-provider" onClick={() => { const next = window.prompt('Provider name'); if (next?.trim()) { setSelected(next.trim()); setProvider({ Name: next.trim(), Type: 'openai', API: '', BaseURL: '', Models: [], HasAPIKey: false, APIKeyHint: '', RequiresAPIKey: false }); setModels([]) } }}>＋ Add provider</button>
        </aside>
        <div className="settings-form">
          <div className="form-row"><label>Provider name<input value={provider.Name} disabled={config.Providers.some((p) => p.Name === provider.Name)} onChange={(e) => setProvider({ ...provider, Name: e.target.value })} /></label><label>Protocol<select value={provider.Type} onChange={(e) => setProvider({ ...provider, Type: e.target.value })}><option value="">Provider default</option><option value="openai">OpenAI compatible</option><option value="anthropic">Anthropic</option><option value="gemini">Gemini</option></select></label></div>
          <label>Base URL<input value={provider.BaseURL} onChange={(e) => setProvider({ ...provider, BaseURL: e.target.value })} placeholder="Leave blank for the official endpoint" /></label>
          <label>API key<input type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder={provider.HasAPIKey ? `Keep current ${provider.APIKeyHint}` : 'Enter a key'} /></label>
          <div className="model-editor"><div className="model-heading"><span>Configured models</span><button onClick={() => setModels([...models, { name: '', context_window: 0 }])}>＋ Add model</button></div>{models.map((model, index) => <div className="model-row" key={index}><input value={model.name} onChange={(e) => setModels(models.map((m, i) => i === index ? { ...m, name: e.target.value } : m))} placeholder="Model ID" /><input type="number" value={model.context_window || ''} onChange={(e) => setModels(models.map((m, i) => i === index ? { ...m, context_window: Number(e.target.value) } : m))} placeholder="Context window" /><button onClick={() => setModels(models.filter((_, i) => i !== index))}>×</button></div>)}</div>
          <div className="default-model"><label>Active model<select value={config.DefaultProvider} onChange={(e) => { const first = config.Providers.find((p) => p.Name === e.target.value)?.Models[0]?.name; if (first) void act(() => api.switchModel(e.target.value, first), 'Default provider switched.') }} disabled={saving}>{config.Providers.map((p) => <option key={p.Name}>{p.Name}</option>)}</select></label><label>Model<select value={config.DefaultModel} onChange={(e) => void act(() => api.switchModel(config.DefaultProvider, e.target.value), 'Default model switched.')} disabled={saving}>{config.Providers.find((p) => p.Name === config.DefaultProvider)?.Models.map((m) => <option key={m.name}>{m.name}</option>)}</select></label></div>
          {error && <p className="form-error">{error}</p>}
          <div className="modal-actions"><button className="button ghost" disabled={saving || !models[0]?.name} onClick={() => void act(() => api.testProvider(draft()), 'Connection test passed.')}>Test connection</button><button className="button primary" disabled={saving || !provider.Name || !models.length} onClick={() => void act(() => api.configureProvider(draft()), 'Provider configuration saved.')}>{saving ? 'Working…' : 'Save changes'}</button></div>
        </div>
      </div>}
    </Modal>
  )
}

function ImportModal({ event, onClose, onImported }: { event: any; onClose: () => void; onImported: () => void }) {
  const [file, setFile] = useState<File | null>(null)
  const [guidance, setGuidance] = useState('')
  const [storyResolution, setStoryResolution] = useState('')
  const [continueAfter, setContinueAfter] = useState(false)
  const [started, setStarted] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function submit(eventForm: FormEvent) {
    eventForm.preventDefault(); if (!file) return
    setError(null)
    try { await api.importNovel(file, { guidance, storyResolution, continueAfter }); setStarted(true) } catch (err) { setError(messageOf(err)) }
  }
  async function resume(options: { acceptSegmentation?: boolean; storyResolution?: string }) {
    setError(null)
    try { await api.resumeImport({ ...options, continueAfter }) } catch (err) { setError(messageOf(err)) }
  }
  useEffect(() => { if (event?.stage === 'done') onImported() }, [event?.stage, onImported])
  const percent = event?.total ? Math.round((event.current / event.total) * 100) : 0
  return <Modal title="Import an existing novel" subtitle="Upload UTF-8 or GB18030 text. The engine will detect chapters and rebuild story memory." onClose={onClose}>
    {!started ? <form className="stack-form" onSubmit={submit}>
      <label className="file-drop"><input type="file" accept=".txt,text/plain" onChange={(e) => setFile(e.target.files?.[0] || null)} /><span>⇧</span><b>{file?.name || 'Choose a manuscript file'}</b><small>TXT · up to 64 MB</small></label>
      <label>Segmentation guidance<textarea value={guidance} onChange={(e) => setGuidance(e.target.value)} placeholder="Optional: Interludes should be treated as standalone chapters…" /></label>
      <div className="form-row"><label>Story status<select value={storyResolution} onChange={(e) => setStoryResolution(e.target.value)}><option value="">Let AI decide</option><option value="open">Ongoing / open</option><option value="closed">Finished / closed</option></select></label><label className="check-label"><input type="checkbox" checked={continueAfter} onChange={(e) => setContinueAfter(e.target.checked)} />Continue writing after import</label></div>
      {error && <p className="form-error">{error}</p>}<div className="modal-actions"><button className="button ghost" type="button" onClick={onClose}>Cancel</button><button className="button primary" disabled={!file}>Start import</button></div>
    </form> : <div className="import-progress"><span className={event?.stage === 'error' ? 'error' : ''}>{event?.stage === 'done' ? '✓' : event?.stage === 'error' ? '!' : '↻'}</span><h3>{humanStage(event?.stage || 'ingesting')}</h3><p>{event?.error || event?.message || 'Preparing your manuscript…'}</p><div className="progress-track large"><i style={{ width: `${event?.stage === 'done' ? 100 : percent}%` }} /></div><small>{event?.total ? `${event.current} / ${event.total}` : 'This can take a while for long books.'}</small>{error && <p className="form-error">{error}</p>}{event?.stage === 'awaiting_confirmation' && <button className="button primary" onClick={() => void resume({ acceptSegmentation: true })}>Confirm chapter structure</button>}{event?.stage === 'awaiting_story_status' && <div className="resume-choices"><button className="button ghost" onClick={() => void resume({ storyResolution: 'open' })}>Story continues</button><button className="button primary" onClick={() => void resume({ storyResolution: 'closed' })}>Story is complete</button></div>}{(event?.stage === 'done' || event?.stage === 'error') && <button className="button primary" onClick={onClose}>Done</button>}</div>}
  </Modal>
}

function ExportModal({ total, onClose }: { total: number; onClose: () => void }) {
  const [format, setFormat] = useState<'txt' | 'epub'>('epub')
  const [from, setFrom] = useState(1)
  const [to, setTo] = useState(total)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  return <Modal title="Export manuscript" subtitle="Create a reader-ready file from completed chapters." onClose={onClose}>
    <div className="format-picker"><button className={format === 'epub' ? 'active' : ''} onClick={() => setFormat('epub')}><span>EPUB</span><b>E-book</b><small>Table of contents and reader styling</small></button><button className={format === 'txt' ? 'active' : ''} onClick={() => setFormat('txt')}><span>TXT</span><b>Plain text</b><small>Simple, portable UTF-8 manuscript</small></button></div>
    <div className="form-row"><label>From chapter<input type="number" min="1" max={total} value={from} onChange={(e) => setFrom(Number(e.target.value))} /></label><label>To chapter<input type="number" min="1" max={total} value={to} onChange={(e) => setTo(Number(e.target.value))} /></label></div>
    {error && <p className="form-error">{error}</p>}<div className="modal-actions"><button className="button ghost" onClick={onClose}>Cancel</button><button className="button primary" disabled={busy || from > to} onClick={async () => { setBusy(true); setError(null); try { await api.exportNovel(format, from, to); onClose() } catch (err) { setError(messageOf(err)); setBusy(false) } }}>{busy ? 'Preparing…' : `Download .${format}`}</button></div>
  </Modal>
}

function CoCreateModal({ stage, onClose, onStarted }: { stage: boolean; onClose: () => void; onStarted: () => void }) {
  type ChatMessage = CoCreateMessage & { display?: string }
  const [history, setHistory] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [draft, setDraft] = useState('')
  const [suggestions, setSuggestions] = useState<string[]>([])
  const [ready, setReady] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function send(text = input) {
    if (!text.trim() || busy) return
    const next = [...history, { role: 'user', content: text.trim() } as CoCreateMessage]
    setHistory(next); setInput(''); setBusy(true); setError(null)
    try {
      const reply = await api.coCreateRound(next, stage)
      setHistory([...next, { role: 'assistant', content: reply.Raw || reply.Message, display: reply.Message }])
      setDraft(reply.Prompt || draft); setReady(reply.Ready); setSuggestions(reply.Suggestions || [])
    } catch (err) { setError(messageOf(err)) } finally { setBusy(false) }
  }
  async function finish() {
    if (!draft.trim()) return
    setBusy(true); setError(null)
    try { if (stage) await api.resumeFromCoCreate(draft); else await api.start(draft); onStarted() } catch (err) { setError(messageOf(err)); setBusy(false) }
  }
  async function close() { if (stage) await api.cancelCoCreate().catch(() => undefined); onClose() }
  return <Modal title={stage ? 'Plan the next stage together' : 'Co-create your novel'} subtitle={stage ? 'The engine is paused while you shape the next direction.' : 'Talk through the idea; the co-creator keeps a production-ready brief for you.'} onClose={() => void close()} wide>
    <div className="cocreate-layout"><div className="conversation"><div className="messages">{!history.length && <div className="assistant-intro"><span>✦</span><div><b>Let’s shape the story.</b><p>{stage ? 'What would you like to change or explore in the next stage?' : 'Tell me the seed of your idea—even a mood, character, or single scene is enough.'}</p></div></div>}{history.map((message, index) => <div key={index} className={`message ${message.role}`}><small>{message.role === 'user' ? 'You' : 'Co-creator'}</small><p>{message.display || message.content}</p></div>)}{busy && <div className="typing"><i /><i /><i /></div>}</div>{suggestions.length > 0 && <div className="suggestions">{suggestions.map((value) => <button key={value} onClick={() => void send(value)}>{value}</button>)}</div>}<form onSubmit={(e) => { e.preventDefault(); void send() }}><textarea value={input} onChange={(e) => setInput(e.target.value)} placeholder="Reply to your co-creator…" /><button className="button primary" disabled={!input.trim() || busy}>Send</button></form></div><aside className="brief-panel"><span className="eyebrow">LIVE CREATIVE BRIEF</span>{draft ? <pre>{draft}</pre> : <p>Your premise, characters, tone and constraints will be compiled here as you talk.</p>}<div className={`readiness ${ready ? 'ready' : ''}`}><i />{ready ? 'Ready for the writing engine' : 'Still gathering key details'}</div><button className="button primary" disabled={!draft.trim() || busy} onClick={() => void finish()}>{stage ? 'Apply plan & resume' : 'Start writing from brief'} →</button></aside></div>{error && <p className="form-error modal-error">{error}</p>}
  </Modal>
}

function Modal({ title, subtitle, onClose, wide, children }: { title: string; subtitle?: string; onClose: () => void; wide?: boolean; children: ReactNode }) {
  return <div className="modal-backdrop" role="presentation"><section className={`modal ${wide ? 'wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}><header><div><h2>{title}</h2>{subtitle && <p>{subtitle}</p>}</div><button onClick={onClose} aria-label="Close">×</button></header><div className="modal-body">{children}</div></section></div>
}

function StatusBadge({ value }: { value: string }) { return <span className={`status-badge ${value}`}><i />{value}</span> }
function messageOf(error: unknown) { return error instanceof Error ? error.message : String(error) }
function formatNumber(value: number) { return new Intl.NumberFormat('en-US').format(value) }
function compactNumber(value: number) { return value >= 1000 ? `${(value / 1000).toFixed(1)}k` : String(value) }
function formatTime(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }
function humanStage(value: string) { return ({ ingesting: 'Reading manuscript', segmenting: 'Detecting chapters', awaiting_confirmation: 'Confirm detected chapters', analyzing: 'Building story memory', synthesizing: 'Reconstructing foundation', awaiting_story_status: 'Is the story complete?', validating: 'Validating structure', publishing: 'Publishing chapters', done: 'Import complete', error: 'Import stopped' } as Record<string, string>)[value] || 'Importing manuscript' }
