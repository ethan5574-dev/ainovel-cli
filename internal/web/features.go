package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/exp"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/store"
)

const maxImportBytes = 64 << 20

type modelDraftRequest struct {
	Provider     string                  `json:"provider"`
	Type         string                  `json:"type"`
	API          string                  `json:"api"`
	BaseURL      string                  `json:"baseURL"`
	Models       []bootstrap.ModelConfig `json:"models"`
	APIKeyAction host.APIKeyAction       `json:"apiKeyAction"`
	APIKey       string                  `json:"apiKey"`
	TestModel    string                  `json:"testModel"`
}

func (r modelDraftRequest) hostDraft() host.ModelConfigurationDraft {
	return host.ModelConfigurationDraft{
		Provider: r.Provider, Type: r.Type, API: r.API, BaseURL: r.BaseURL,
		Models: r.Models, APIKeyAction: r.APIKeyAction, APIKey: r.APIKey,
	}
}

func (s *Server) handleConfigureProvider(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req modelDraftRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := rt.ConfigureModels(req.hostDraft()); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, rt.ModelConfiguration())
}

func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req modelDraftRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := rt.TestModelConnection(ctx, req.hostDraft(), req.TestModel); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type switchModelRequest struct {
	Role     string `json:"role"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (s *Server) handleSwitchModel(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req switchModelRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := rt.SwitchModel(strings.TrimSpace(req.Role), strings.TrimSpace(req.Provider), strings.TrimSpace(req.Model)); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, rt.ModelConfiguration())
}

type chapterListItem struct {
	Chapter   int    `json:"chapter"`
	Title     string `json:"title"`
	CoreEvent string `json:"coreEvent,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Status    string `json:"status"`
	WordCount int    `json:"wordCount"`
	Volume    int    `json:"volume,omitempty"`
	Arc       int    `json:"arc,omitempty"`
}

type bookResponse struct {
	Name     string            `json:"name"`
	Premise  string            `json:"premise"`
	Phase    domain.Phase      `json:"phase"`
	Chapters []chapterListItem `json:"chapters"`
}

type chapterLocation struct{ volume, arc int }

func loadBook(rt *host.Host) (bookResponse, error) {
	st := store.NewStore(rt.Dir())
	progress, err := st.Progress.Load()
	if err != nil {
		return bookResponse{}, err
	}
	book, err := st.Book.Load()
	if err != nil {
		return bookResponse{}, err
	}
	premise, err := st.Outline.LoadPremise()
	if err != nil {
		return bookResponse{}, err
	}
	outline, err := st.Outline.LoadOutline()
	if err != nil {
		return bookResponse{}, err
	}
	locations := make(map[int]chapterLocation)
	if volumes, loadErr := st.Outline.LoadLayeredOutline(); loadErr == nil {
		ch := 1
		for _, volume := range volumes {
			for _, arc := range volume.Arcs {
				for range arc.Chapters {
					locations[ch] = chapterLocation{volume: volume.Index, arc: arc.Index}
					ch++
				}
			}
		}
	}

	completed := make(map[int]bool)
	total := len(outline)
	// Keep the API collection-shaped even when planning has not produced an outline yet.
	response := bookResponse{Premise: premise, Chapters: make([]chapterListItem, 0, total)}
	if book != nil {
		response.Name = book.Title
	}
	if progress != nil {
		response.Phase = progress.Phase
		for _, ch := range progress.CompletedChapters {
			completed[ch] = true
		}
		if progress.TotalChapters > total {
			total = progress.TotalChapters
		}
	}
	for i := 1; i <= total; i++ {
		item := chapterListItem{Chapter: i, Status: "planned"}
		if i <= len(outline) {
			item.Title = outline[i-1].Title
			item.CoreEvent = outline[i-1].CoreEvent
		}
		if loc, exists := locations[i]; exists {
			item.Volume, item.Arc = loc.volume, loc.arc
		}
		if progress != nil {
			item.WordCount = progress.ChapterWordCounts[i]
			switch {
			case completed[i]:
				item.Status = "completed"
			case progress.InProgressChapter == i:
				item.Status = "writing"
			}
		}
		if summary, loadErr := st.Summaries.LoadSummary(i); loadErr == nil && summary != nil {
			if strings.TrimSpace(summary.Title) != "" {
				item.Title = summary.Title
			}
			item.Summary = summary.Summary
		}
		response.Chapters = append(response.Chapters, item)
	}
	return response, nil
}

func (s *Server) handleBook(w http.ResponseWriter, _ *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	book, err := loadBook(rt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, book)
}

func (s *Server) handleChapter(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	chapter, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/chapters/"))
	if err != nil || chapter <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid chapter number"))
		return
	}
	st := store.NewStore(rt.Dir())
	content, err := st.Drafts.LoadChapterText(chapter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	status := "completed"
	if content == "" {
		content, err = st.Drafts.LoadDraft(chapter)
		status = "draft"
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	outline, _ := st.Outline.GetChapterOutline(chapter)
	summary, _ := st.Summaries.LoadSummary(chapter)
	plan, _ := st.Drafts.LoadChapterPlan(chapter)
	writeJSON(w, http.StatusOK, map[string]any{
		"chapter": chapter, "status": status, "content": content,
		"outline": outline, "summary": summary, "plan": plan,
	})
}

type exportRequest struct {
	Format string `json:"format"`
	From   int    `json:"from"`
	To     int    `json:"to"`
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req exportRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	format := exp.Format(strings.ToLower(strings.TrimSpace(req.Format)))
	if format != exp.FormatTXT && format != exp.FormatEPUB {
		writeError(w, http.StatusBadRequest, fmt.Errorf("format must be txt or epub"))
		return
	}
	tmpDir, err := os.MkdirTemp("", "ainovel-export-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer os.RemoveAll(tmpDir)
	outPath := filepath.Join(tmpDir, "novel."+string(format))
	result, err := rt.Export(r.Context(), exp.Options{Format: format, OutPath: outPath, From: req.From, To: req.To})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	name := "novel"
	if book, _ := store.NewStore(rt.Dir()).Book.Load(); book != nil && strings.TrimSpace(book.Title) != "" {
		name = safeFilename(book.Title)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, name, format))
	if format == exp.FormatEPUB {
		w.Header().Set("Content-Type", "application/epub+zip")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func safeFilename(value string) string {
	value = strings.TrimSpace(filepath.Base(value))
	value = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, value)
	if value == "" || value == "." {
		return "novel"
	}
	return value
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+(1<<20))
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid import upload: %w", err))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("file is required"))
		return
	}
	defer file.Close()
	uploadDir := filepath.Join(rt.Dir(), "meta", "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	ext := filepath.Ext(safeFilename(header.Filename))
	if ext == "" {
		ext = ".txt"
	}
	dst, err := os.CreateTemp(uploadDir, "import-*"+ext)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	path := dst.Name()
	written, copyErr := io.Copy(dst, io.LimitReader(file, maxImportBytes+1))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil || written > maxImportBytes {
		_ = os.Remove(path)
		writeError(w, http.StatusBadRequest, fmt.Errorf("failed to save import file or file is too large"))
		return
	}
	opts := imp.Options{
		SourcePath: path, AutoConfirm: r.FormValue("autoConfirm") != "false",
		StoryResolution: strings.TrimSpace(r.FormValue("storyResolution")),
		ContinueAfter:   r.FormValue("continueAfter") == "true",
		Guidance:        strings.TrimSpace(r.FormValue("guidance")),
	}
	events, err := rt.ImportFrom(context.Background(), opts)
	if err != nil {
		_ = os.Remove(path)
		writeError(w, http.StatusConflict, err)
		return
	}
	s.forwardImportEvents(events, func() { _ = os.Remove(path) })
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

type importResumeRequest struct {
	AcceptSegmentation bool   `json:"acceptSegmentation"`
	StoryResolution    string `json:"storyResolution"`
	ContinueAfter      bool   `json:"continueAfter"`
}

func (s *Server) handleImportResume(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req importResumeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	events, err := rt.ImportFrom(context.Background(), imp.Options{
		AcceptSegmentation: req.AcceptSegmentation,
		StoryResolution:    strings.TrimSpace(req.StoryResolution),
		ContinueAfter:      req.ContinueAfter,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.forwardImportEvents(events, nil)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) forwardImportEvents(events <-chan imp.Event, done func()) {
	go func() {
		if done != nil {
			defer done()
		}
		for event := range events {
			errText := ""
			if event.Err != nil {
				errText = event.Err.Error()
			}
			s.publish("import", map[string]any{
				"stage": event.Stage, "current": event.Current, "total": event.Total,
				"message": event.Message, "level": event.Level, "key": event.Key,
				"retryAt": event.RetryAt, "error": errText, "continued": event.Continued,
			}, true)
		}
	}()
}

type coCreateRoundRequest struct {
	History []host.CoCreateMessage `json:"history"`
	Stage   bool                   `json:"stage"`
}

func (s *Server) handleCoCreateRound(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req coCreateRoundRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(req.History) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("co-create history is required"))
		return
	}
	progress := func(kind, text string) { s.publish("cocreate", map[string]string{"kind": kind, "text": text}, false) }
	var reply host.CoCreateReply
	var err error
	if req.Stage {
		reply, err = rt.StageCoCreateStream(r.Context(), req.History, progress)
	} else {
		reply, err = rt.CoCreateStream(r.Context(), req.History, progress)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) handleCoCreatePause(w http.ResponseWriter, _ *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	if !rt.PauseForCoCreate() {
		writeError(w, http.StatusConflict, fmt.Errorf("cannot enter co-create in the current state"))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

type coCreateResumeRequest struct {
	Draft string `json:"draft"`
}

func (s *Server) handleCoCreateResume(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	var req coCreateResumeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := rt.ResumeFromCoCreate(req.Draft); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) handleCoCreateCancel(w http.ResponseWriter, _ *http.Request) {
	rt, ok := s.rt()
	if !ok {
		writeSetupRequired(w)
		return
	}
	rt.CancelCoCreate()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
