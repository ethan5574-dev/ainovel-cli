package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func setupLayered(t *testing.T, volumes []domain.VolumeOutline) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}
	return s
}

func TestCheckArcBoundaryNeedsNewVolume(t *testing.T) {
	// Only 1 volume, 1 arc, 1 chapter, and not Final -> should trigger NeedsNewVolume
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Volume 1", Theme: "Starting Point",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "First Arc", Goal: "Goal",
			Chapters: []domain.OutlineEntry{{Title: "Chapter 1", CoreEvent: "Opening", Hook: "Continue"}},
		}},
	}})

	b, err := s.Outline.CheckArcBoundary(1) // Chapter 1 = last chapter of the arc/volume
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if b == nil {
		t.Fatal("expected boundary, got nil")
	}
	if !b.IsArcEnd || !b.IsVolumeEnd {
		t.Fatalf("expected arc+volume end, got arc=%v vol=%v", b.IsArcEnd, b.IsVolumeEnd)
	}
	if !b.NeedsNewVolume {
		t.Fatal("expected NeedsNewVolume=true")
	}
	if b.NextVolume != 0 || b.NextArc != 0 {
		t.Fatalf("expected no next, got vol=%d arc=%d", b.NextVolume, b.NextArc)
	}
}

func TestCheckArcBoundaryLastVolumeRequiresDecision(t *testing.T) {
	// Single volume last chapter -> triggers NeedsNewVolume, prompting Router to let architect choose:
	// append_volume to continue writing / complete_book to finish.
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Single Volume", Theme: "Theme",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "Single Arc", Goal: "Resolution",
			Chapters: []domain.OutlineEntry{{Title: "Final Chapter", CoreEvent: "Ending", Hook: "None"}},
		}},
	}})

	b, err := s.Outline.CheckArcBoundary(1)
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if !b.NeedsNewVolume {
		t.Fatal("expected NeedsNewVolume=true at last expanded chapter")
	}
	if b.HasNextArc() {
		t.Fatal("expected no next arc")
	}
}

func TestCheckArcBoundaryNextArcInSameVolume(t *testing.T) {
	// 2 arcs: end of arc 1 should point to arc 2, without triggering NeedsNewVolume
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Volume 1", Theme: "Starting Point",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "First Arc", Goal: "Goal", Chapters: []domain.OutlineEntry{{Title: "Chapter 1", CoreEvent: "Event", Hook: "Hook"}}},
			{Index: 2, Title: "Second Arc", Goal: "Goal 2", EstimatedChapters: 10},
		},
	}})

	b, err := s.Outline.CheckArcBoundary(1)
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if !b.IsArcEnd {
		t.Fatal("expected arc end")
	}
	if b.IsVolumeEnd {
		t.Fatal("expected not volume end (second arc exists)")
	}
	if b.NeedsNewVolume {
		t.Fatal("expected NeedsNewVolume=false")
	}
	if b.NextVolume != 1 || b.NextArc != 2 {
		t.Fatalf("expected next vol=1 arc=2, got vol=%d arc=%d", b.NextVolume, b.NextArc)
	}
	if !b.NeedsExpansion {
		t.Fatal("expected NeedsExpansion=true for skeleton arc")
	}
}

func TestAppendVolumeValidation(t *testing.T) {
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Volume 1", Theme: "Starting Point",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "First Arc", Goal: "Goal",
			Chapters: []domain.OutlineEntry{{Title: "Chapter", CoreEvent: "Event", Hook: "Hook"}},
		}},
	}})

	validVol := domain.VolumeOutline{
		Index: 2, Title: "Volume 2", Theme: "Progression",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "Arc 1", Goal: "Goal",
			Chapters: []domain.OutlineEntry{{Title: "New Chapter", CoreEvent: "Advance", Hook: "Hook"}},
		}},
	}

	// Normal append should succeed
	if err := s.AppendVolume(validVol); err != nil {
		t.Fatalf("AppendVolume valid: %v", err)
	}

	// Index not increasing -> fail
	if err := s.AppendVolume(domain.VolumeOutline{
		Index: 1, Title: "Duplicate", Theme: "x",
		Arcs: []domain.ArcOutline{{Index: 1, Title: "Arc", Goal: "g", Chapters: []domain.OutlineEntry{{Title: "ch", CoreEvent: "e", Hook: "h"}}}},
	}); err == nil {
		t.Fatal("expected error for non-increasing index")
	}

	// No arcs -> fail
	if err := s.AppendVolume(domain.VolumeOutline{Index: 3, Title: "Empty", Theme: "x"}); err == nil {
		t.Fatal("expected error for volume with no arcs")
	}

	// First arc without chapters -> fail
	if err := s.AppendVolume(domain.VolumeOutline{
		Index: 3, Title: "Skeleton", Theme: "x",
		Arcs: []domain.ArcOutline{{Index: 1, Title: "Arc", Goal: "g", EstimatedChapters: 10}},
	}); err == nil {
		t.Fatal("expected error for first arc without chapters")
	}
}

// Note: The previous logic that rejected append on Final volumes has been moved to save_foundation layer (Phase=Complete reject),
// see save_foundation_test.go::TestSaveFoundationAppendVolumeRejectsAfterComplete.
// The store layer only keeps structural validations (e.g. index increasing / first arc containing chapters).

func TestSaveAndLoadCompass(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Empty direction should fail
	if err := s.Outline.SaveCompass(domain.StoryCompass{EstimatedScale: "3 volumes"}); err == nil {
		t.Fatal("expected error for empty ending_direction")
	}

	// Normal save
	compass := domain.StoryCompass{
		EndingDirection: "protagonist faces the final choice",
		OpenThreads:     []string{"Clue A", "Relationship B"},
		EstimatedScale:  "estimated 4-6 volumes",
		LastUpdated:     12,
	}
	if err := s.Outline.SaveCompass(compass); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}

	loaded, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected compass, got nil")
	}
	if loaded.EndingDirection != "protagonist faces the final choice" {
		t.Fatalf("expected direction %q, got %q", "protagonist faces the final choice", loaded.EndingDirection)
	}
	if len(loaded.OpenThreads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(loaded.OpenThreads))
	}
}
