package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/tools"
)

//go:embed webui/*
var webUI embed.FS

const defaultAddr = "127.0.0.1:5173"

// Server can start before a runtime exists: on a machine with no saved config,
// bootstrap.NeedsSetup() is true and there's nothing to build a *host.Host from yet.
// runtime attaches at most once, when first-run setup completes (see AttachRuntime).
// Every handler that needs it must go through rt(), not a direct field read.
type Server struct {
	runtime atomic.Pointer[host.Host]
	ui      fs.FS

	mu      sync.Mutex
	clients map[chan message]struct{}
	recent  []message
	tasks   map[string]pendingAsk
	seq     uint64
}

type message struct {
	Event string
	ID    string // SSE `id:` line; empty = omitted. Only durable-queue replay sets this.
	Data  any
}

// eventReplayLimit caps how much durable history a fresh (non-resuming) connection backfills.
const eventReplayLimit = 80

type pendingAsk struct {
	result chan askResult
}

type askResult struct {
	response *tools.AskUserResponse
	err      error
}

type askPayload struct {
	ID        string           `json:"id"`
	Questions []tools.Question `json:"questions"`
}

type textRequest struct {
	Text      string `json:"text"`
	Prompt    string `json:"prompt"`
	Direction string `json:"direction"`
}

type reviewRequest struct {
	Mode string `json:"mode"`
}

type answerRequest struct {
	Answers map[string]string `json:"answers"`
	Notes   map[string]string `json:"notes"`
}

// NewServer builds a Server. runtime may be nil (fresh machine, setup not done yet);
// call AttachRuntime once setup completes to bring it live without restarting the process.
func NewServer(runtime *host.Host) (*Server, error) {
	ui, err := fs.Sub(webUI, "webui")
	if err != nil {
		return nil, fmt.Errorf("load web UI: %w", err)
	}
	s := &Server{
		ui:      ui,
		clients: make(map[chan message]struct{}),
		tasks:   make(map[string]pendingAsk),
	}
	if runtime != nil {
		s.AttachRuntime(runtime)
	}
	return s, nil
}

// AttachRuntime wires a runtime into a Server that started unattached. Safe to call at most
// once per Server (a second call would start a duplicate fanout goroutine).
func (s *Server) AttachRuntime(runtime *host.Host) {
	s.runtime.Store(runtime)
	runtime.AskUser().SetHandler(s.askUser)
	go s.fanout(runtime)
}

// rt returns the attached runtime, or ok=false if setup hasn't completed yet.
func (s *Server) rt() (*host.Host, bool) {
	rt := s.runtime.Load()
	return rt, rt != nil
}

func writeSetupRequired(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, fmt.Errorf("setup required: complete first-run configuration before using this endpoint"))
}

func Run(runtime *host.Host, addr string) error {
	server, err := NewServer(runtime)
	if err != nil {
		return err
	}
	defer func() {
		if rt, ok := server.rt(); ok {
			rt.Close()
		}
	}()
	if strings.TrimSpace(addr) == "" {
		addr = defaultAddr
	}
	return server.Serve(addr)
}

func (s *Server) Serve(addr string) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return server.ListenAndServe()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	// "GET /" is a subtree pattern (Go 1.22+ ServeMux): it matches every path not claimed by
	// a more specific pattern below, including /assets/*.js and /favicon.svg — handleStatic
	// serves whichever embedded file matches the request path, falling back to index.html.
	mux.HandleFunc("GET /", s.handleStatic)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("GET /api/snapshot", s.handleSnapshot)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("POST /api/start", s.handleStart)
	mux.HandleFunc("POST /api/resume", s.handleResume)
	mux.HandleFunc("POST /api/continue", s.handleContinue)
	mux.HandleFunc("POST /api/steer", s.handleSteer)
	mux.HandleFunc("POST /api/abort", s.handleAbort)
	mux.HandleFunc("POST /api/next", s.handleNext)
	mux.HandleFunc("POST /api/reopen", s.handleReopen)
	mux.HandleFunc("POST /api/review", s.handleReview)
	mux.HandleFunc("POST /api/ask/", s.handleAskAnswer)
	return withLocalHeaders(mux)
}

func withLocalHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleStatic serves the embedded Vite build. It first tries the exact requested path
// (covers /assets/index-*.js, /favicon.svg, ...); anything that doesn't match a real file
// falls back to index.html, so client-side routes added in later phases (e.g. /chapters/5)
// resolve to the SPA shell on a full page load instead of 404ing.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	reqPath := strings.TrimPrefix(r.URL.Path, "/")
	if reqPath != "" {
		if data, err := fs.ReadFile(s.ui, reqPath); err == nil {
			http.ServeContent(w, r, reqPath, time.Time{}, bytes.NewReader(data))
			return
		}
	}
	data, err := fs.ReadFile(s.ui, "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	dir := ""
	if rt, ok := s.rt(); ok {
		dir = rt.Dir()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "dir": dir})
}

// handleSetupStatus reports whether first-run configuration is still needed, so the
// frontend can route between the onboarding wizard and the dashboard before an SSE
// connection to a (possibly still-unattached) runtime is worth attempting.
func (s *Server) handleSetupStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"needsSetup": bootstrap.NeedsSetup()})
}

func (s *Server) handleSnapshot(w http.ResponseWriter, _ *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	writeJSON(w, http.StatusOK, rt.Snapshot())
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	writeJSON(w, http.StatusOK, rt.ModelConfiguration())
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	flusher, okFlush := w.(http.Flusher)
	if !okFlush {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is not supported by this server"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	client, recent := s.addClient()
	defer s.removeClient(client)

	_ = writeSSE(w, message{Event: "snapshot", Data: rt.Snapshot()})

	// Durable catch-up. On a fresh connection (afterSeq==0) this backfills the last
	// eventReplayLimit items so a newly opened tab isn't staring at an empty log; on a
	// reconnect it resumes exactly where the client left off — either the browser's own
	// automatic retry (Last-Event-ID header, updated natively on every id-bearing message)
	// or an explicit ?after= the frontend persisted itself before a full page reload
	// (EventSource has no way to set request headers on initial construction, so a hard
	// refresh can't rely on Last-Event-ID alone). Only finished-state DISPATCH/TOOL/DECISION
	// rows and non-call events (SYSTEM/ERROR/...) live in this queue — in-progress "still
	// running" rows are deliberately never persisted (see observer.emitAndLog's comment),
	// so a reconnect mid-call won't show a stale "in progress" row, only the eventual
	// finished one, which is correct.
	afterSeq := replayAfterSeq(r)
	if items, err := rt.ReplayQueue(afterSeq); err == nil {
		if afterSeq <= 0 && len(items) > eventReplayLimit {
			items = items[len(items)-eventReplayLimit:]
		}
		for _, item := range items {
			if item.Kind != domain.RuntimeQueueUIEvent {
				continue
			}
			if !writeSSE(w, message{Event: "event", ID: strconv.FormatInt(item.Seq, 10), Data: item.Payload}) {
				return
			}
		}
	}

	for _, msg := range recent {
		if !writeSSE(w, msg) {
			return
		}
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-client:
			if !ok || !writeSSE(w, msg) {
				return
			}
			flusher.Flush()
		}
	}
}

func replayAfterSeq(r *http.Request) int64 {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	if v := r.URL.Query().Get("after"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func (s *Server) addClient() (chan message, []message) {
	client := make(chan message, 128)
	s.mu.Lock()
	s.clients[client] = struct{}{}
	recent := append([]message(nil), s.recent...)
	s.mu.Unlock()
	return client, recent
}

func (s *Server) removeClient(client chan message) {
	s.mu.Lock()
	if _, ok := s.clients[client]; ok {
		delete(s.clients, client)
		close(client)
	}
	s.mu.Unlock()
}

func (s *Server) publish(event string, data any, retain bool) {
	msg := message{Event: event, Data: data}
	s.mu.Lock()
	if retain {
		s.recent = append(s.recent, msg)
		if len(s.recent) > 80 {
			s.recent = s.recent[len(s.recent)-80:]
		}
	}
	for client := range s.clients {
		select {
		case client <- msg:
		default:
			// A slow browser should not block the engine's event projection.
		}
	}
	s.mu.Unlock()
}

func (s *Server) fanout(rt *host.Host) {
	events := rt.Events()
	stream := rt.Stream()
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for events != nil || stream != nil {
		select {
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			// retain=false: durable-queue replay (handleEvents) now covers reconnect
			// catch-up for this category, so the in-memory ring buffer no longer needs
			// to hold it (see eventReplayLimit / replayAfterSeq).
			s.publish("event", event, false)
			s.publish("snapshot", rt.Snapshot(), false)
		case delta, ok := <-stream:
			if !ok {
				stream = nil
				continue
			}
			if delta == host.StreamClearSentinel {
				s.publish("clear", true, false)
			} else {
				s.publish("delta", delta, false)
			}
		case <-ticker.C:
			s.publish("snapshot", rt.Snapshot(), false)
		}
	}
}

func (s *Server) askUser(ctx context.Context, questions []tools.Question) (*tools.AskUserResponse, error) {
	id := fmt.Sprintf("ask-%d", atomic.AddUint64(&s.seq, 1))
	result := make(chan askResult, 1)
	s.mu.Lock()
	s.tasks[id] = pendingAsk{result: result}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.tasks, id)
		s.mu.Unlock()
	}()
	s.publish("ask", askPayload{ID: id, Questions: questions}, true)

	select {
	case answer := <-result:
		return answer.response, answer.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Server) handleAskAnswer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/ask/")
	if id == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("ask id is required"))
		return
	}
	var req answerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	pending, ok := s.tasks[id]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("ask request not found"))
		return
	}
	pending.result <- askResult{response: &tools.AskUserResponse{Answers: req.Answers, Notes: req.Notes}}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req textRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	text := strings.TrimSpace(req.Prompt)
	if text == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("prompt is required"))
		return
	}
	if err := rt.PrepareUserRules(text); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	if err := rt.StartPrepared(text); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleResume(w http.ResponseWriter, _ *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	label, err := rt.Resume()
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "label": label})
}

func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	text, err := readRequestText(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := rt.Continue(text); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleSteer(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	text, err := readRequestText(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := rt.Steer(text); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleAbort(w http.ResponseWriter, _ *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	if !rt.Abort() {
		writeError(w, http.StatusConflict, fmt.Errorf("nothing is currently running"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleNext(w http.ResponseWriter, _ *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	if err := rt.AdvanceOneChapter(); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleReopen(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req textRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if err := rt.Reopen(req.Direction); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req reviewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var mode domain.ChapterAdvanceMode
	switch strings.ToLower(strings.TrimSpace(req.Mode)) {
	case "auto":
		mode = domain.ChapterAdvanceAuto
	case "review":
		mode = domain.ChapterAdvanceReview
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("mode must be auto or review"))
		return
	}
	if err := rt.SetAdvanceMode(mode); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func readRequestText(r *http.Request) (string, error) {
	var req textRequest
	if err := decodeJSON(r, &req); err != nil {
		return "", err
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	return text, nil
}

func decodeJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func writeSSE(w http.ResponseWriter, msg message) bool {
	data, err := json.Marshal(msg.Data)
	if err != nil {
		return false
	}
	idLine := ""
	if msg.ID != "" {
		idLine = "id: " + msg.ID + "\n"
	}
	if _, err := fmt.Fprintf(w, "%sevent: %s\ndata: %s\n\n", idLine, msg.Event, data); err != nil {
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "error": err.Error()})
}
