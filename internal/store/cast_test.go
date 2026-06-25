package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func newCastTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s
}

func TestCastMergeAppearances_NewEntries(t *testing.T) {
	s := newCastTestStore(t)
	intros := []domain.CastIntro{{Name: "Lao Zhou", BriefRole: "Innkeeper"}}
	if err := s.Cast.MergeAppearances(5, []string{"Lao Zhou", "A Yun"}, intros, nil); err != nil {
		t.Fatalf("MergeAppearances: %v", err)
	}

	entries, err := s.Cast.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.FirstSeenChapter != 5 || e.LastSeenChapter != 5 || e.AppearanceCount != 1 {
			t.Errorf("entry %s: unexpected appearance fields %+v", e.Name, e)
		}
		if e.Name == "Lao Zhou" && e.BriefRole != "Innkeeper" {
			t.Errorf("expected BriefRole Innkeeper for Lao Zhou, got %q", e.BriefRole)
		}
		if e.Name == "A Yun" && e.BriefRole != "" {
			t.Errorf("A Yun has no intro, BriefRole should be empty, got %q", e.BriefRole)
		}
	}
}

func TestCastMergeAppearances_AccumulatesOnRepeat(t *testing.T) {
	s := newCastTestStore(t)
	if err := s.Cast.MergeAppearances(5, []string{"Lao Zhou"}, nil, nil); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	if err := s.Cast.MergeAppearances(8, []string{"Lao Zhou"}, nil, nil); err != nil {
		t.Fatalf("second merge: %v", err)
	}

	entries, _ := s.Cast.Load()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.FirstSeenChapter != 5 || e.LastSeenChapter != 8 || e.AppearanceCount != 2 {
		t.Fatalf("expected first=5,last=8,count=2; got %+v", e)
	}
	if len(e.AppearanceChapters) != 2 || e.AppearanceChapters[0] != 5 || e.AppearanceChapters[1] != 8 {
		t.Errorf("AppearanceChapters wrong: %v", e.AppearanceChapters)
	}
}

func TestCastMergeAppearances_IsIdempotent(t *testing.T) {
	s := newCastTestStore(t)
	if err := s.Cast.MergeAppearances(5, []string{"Lao Zhou"}, nil, nil); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	// Duplicate trigger for the same chapter commit (crash recovery or rewrite scenarios)
	if err := s.Cast.MergeAppearances(5, []string{"Lao Zhou"}, nil, nil); err != nil {
		t.Fatalf("second merge: %v", err)
	}

	entries, _ := s.Cast.Load()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].AppearanceCount != 1 {
		t.Errorf("expected AppearanceCount=1 after duplicate, got %d", entries[0].AppearanceCount)
	}
}

func TestCastMergeAppearances_FiltersCoreCharacters(t *testing.T) {
	s := newCastTestStore(t)
	core := map[string]bool{"Lin Mo": true, "Li Qingyan": true}
	if err := s.Cast.MergeAppearances(3, []string{"Lin Mo", "Li Qingyan", "Lao Zhou"}, nil, core); err != nil {
		t.Fatalf("MergeAppearances: %v", err)
	}

	entries, _ := s.Cast.Load()
	if len(entries) != 1 || entries[0].Name != "Lao Zhou" {
		t.Fatalf("expected only Lao Zhou in ledger, got %+v", entries)
	}
}

func TestCastMergeAppearances_BackfillsBriefRole(t *testing.T) {
	s := newCastTestStore(t)
	// Chapter 5 introduces Lao Zhou, but the Writer forgets to fill in brief_role
	if err := s.Cast.MergeAppearances(5, []string{"Lao Zhou"}, nil, nil); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	// Reappears in Chapter 8, this time the Writer supplements the brief_role
	intros := []domain.CastIntro{{Name: "Lao Zhou", BriefRole: "Innkeeper"}}
	if err := s.Cast.MergeAppearances(8, []string{"Lao Zhou"}, intros, nil); err != nil {
		t.Fatalf("second merge: %v", err)
	}

	entries, _ := s.Cast.Load()
	if entries[0].BriefRole != "Innkeeper" {
		t.Errorf("expected BriefRole Innkeeper backfilled, got %q", entries[0].BriefRole)
	}
}

func TestCastMergeAppearances_NoOverwriteBriefRole(t *testing.T) {
	s := newCastTestStore(t)
	// Chapter 5 establishes BriefRole=Innkeeper
	if err := s.Cast.MergeAppearances(5,
		[]string{"Lao Zhou"},
		[]domain.CastIntro{{Name: "Lao Zhou", BriefRole: "Innkeeper"}},
		nil,
	); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	// In Chapter 8, the Writer mistakenly passes a different BriefRole (should not overwrite)
	if err := s.Cast.MergeAppearances(8,
		[]string{"Lao Zhou"},
		[]domain.CastIntro{{Name: "Lao Zhou", BriefRole: "Casino Thug"}},
		nil,
	); err != nil {
		t.Fatalf("second merge: %v", err)
	}

	entries, _ := s.Cast.Load()
	if entries[0].BriefRole != "Innkeeper" {
		t.Errorf("expected BriefRole NOT overwritten, got %q", entries[0].BriefRole)
	}
}

func TestCastRecentActive_OrdersByLastSeen(t *testing.T) {
	s := newCastTestStore(t)
	_ = s.Cast.MergeAppearances(3, []string{"A"}, nil, nil)
	_ = s.Cast.MergeAppearances(10, []string{"B"}, nil, nil)
	_ = s.Cast.MergeAppearances(7, []string{"C"}, nil, nil)

	recent, err := s.Cast.RecentActive(2)
	if err != nil {
		t.Fatalf("RecentActive: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2, got %d", len(recent))
	}
	if recent[0].Name != "B" || recent[1].Name != "C" {
		t.Errorf("expected order B, C; got %s, %s", recent[0].Name, recent[1].Name)
	}
}

func TestCastRecentActive_SkipsPromoted(t *testing.T) {
	s := newCastTestStore(t)
	if err := s.Cast.Save([]domain.CastEntry{
		{Name: "Promoted Core", LastSeenChapter: 20, AppearanceCount: 8, Promoted: true},
		{Name: "Active Supporting Character", LastSeenChapter: 18, AppearanceCount: 3},
		{Name: "Another Supporting Character", LastSeenChapter: 15, AppearanceCount: 2},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	recent, err := s.Cast.RecentActive(10)
	if err != nil {
		t.Fatalf("RecentActive: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("expected 2 (Promoted excluded), got %d: %+v", len(recent), recent)
	}
	for _, e := range recent {
		if e.Promoted {
			t.Errorf("Promoted entry leaked into RecentActive: %+v", e)
		}
	}
	if recent[0].Name != "Active Supporting Character" {
		t.Errorf("expected first=Active Supporting Character, got %s", recent[0].Name)
	}
}

func TestCastMergeAppearances_NoOpOnEmpty(t *testing.T) {
	s := newCastTestStore(t)
	if err := s.Cast.MergeAppearances(5, nil, nil, nil); err != nil {
		t.Fatalf("MergeAppearances empty: %v", err)
	}
	if err := s.Cast.MergeAppearances(0, []string{"Lao Zhou"}, nil, nil); err != nil {
		t.Fatalf("MergeAppearances chapter=0: %v", err)
	}
	entries, _ := s.Cast.Load()
	if len(entries) != 0 {
		t.Errorf("expected empty ledger, got %d entries", len(entries))
	}
}
