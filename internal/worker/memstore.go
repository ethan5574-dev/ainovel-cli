package worker

import (
	"fmt"
	"sync"
	"time"
)

// memStore is an in-memory JobStore fake for tests that exercise runner/harvest logic
// without a real Postgres. Not exported: production code always uses PGStore.
type memStore struct {
	mu sync.Mutex

	jobs          map[string]*Job
	novelStatus   map[string]string
	chapters      []ChapterRecord
	usage         []UsageRecord
	heartbeats    int
	completed     []string
	failed        map[string]string
	claimedByOnly string // set by tests that only care whoever claimed

	reviews      []ChapterReviewRecord
	foreshadows  []ForeshadowRecord
	qualityScore map[string]float64
	published    map[string]bool
}

func newMemStore(jobs ...Job) *memStore {
	m := &memStore{
		jobs:         make(map[string]*Job),
		novelStatus:  make(map[string]string),
		failed:       make(map[string]string),
		qualityScore: make(map[string]float64),
		published:    make(map[string]bool),
	}
	for i := range jobs {
		j := jobs[i]
		m.jobs[j.ID] = &j
	}
	return m
}

func (m *memStore) ClaimNextJob(workerID string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimedByOnly = workerID
	for _, j := range m.jobs {
		if j.Status == JobQueued {
			j.Status = JobRunning
			cp := *j
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *memStore) Heartbeat(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[jobID]; !ok {
		return fmt.Errorf("unknown job %s", jobID)
	}
	m.heartbeats++
	return nil
}

func (m *memStore) CompleteJob(jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("unknown job %s", jobID)
	}
	j.Status = JobDone
	m.completed = append(m.completed, jobID)
	return nil
}

func (m *memStore) FailJob(jobID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[jobID]
	if !ok {
		return fmt.Errorf("unknown job %s", jobID)
	}
	j.Status = JobFailed
	m.failed[jobID] = reason
	return nil
}

func (m *memStore) RequeueStale(time.Duration) (int, error) { return 0, nil }

func (m *memStore) RecordUsage(rec UsageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage = append(m.usage, rec)
	return nil
}

func (m *memStore) SaveChapter(rec ChapterRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chapters = append(m.chapters, rec)
	return nil
}

func (m *memStore) UpdateNovelStatus(novelID, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.novelStatus[novelID] = status
	return nil
}

func (m *memStore) SaveChapterReview(rec ChapterReviewRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.reviews {
		if existing.NovelID == rec.NovelID && existing.Chapter == rec.Chapter && existing.Scope == rec.Scope {
			m.reviews[i] = rec
			return nil
		}
	}
	m.reviews = append(m.reviews, rec)
	return nil
}

func (m *memStore) SaveForeshadow(rec ForeshadowRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.foreshadows {
		if existing.NovelID == rec.NovelID && existing.ForeshadowID == rec.ForeshadowID {
			m.foreshadows[i] = rec
			return nil
		}
	}
	m.foreshadows = append(m.foreshadows, rec)
	return nil
}

func (m *memStore) SetQualityGate(novelID string, score float64, publish bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.qualityScore[novelID] = score
	if publish {
		m.published[novelID] = true
	}
	return nil
}

func (m *memStore) TodaySpendUSD() (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var total float64
	for _, rec := range m.usage {
		total += rec.CostUSD
	}
	return total, nil
}

// memChapterStore is an in-memory ChapterStore fake.
type memChapterStore struct {
	mu    sync.Mutex
	saved map[string]string // storageKey -> content
}

func newMemChapterStore() *memChapterStore {
	return &memChapterStore{saved: make(map[string]string)}
}

func (m *memChapterStore) Save(novelID string, chapter int, content string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s/%02d.md", novelID, chapter)
	m.saved[key] = content
	return key, nil
}
