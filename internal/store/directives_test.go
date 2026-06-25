package store

import (
	"fmt"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestDirectivesLoadEmpty(t *testing.T) {
	store := NewStore(t.TempDir())
	list, err := store.Directives.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

func TestDirectivesAddAndLoad(t *testing.T) {
	store := NewStore(t.TempDir())

	list, err := store.Directives.Add(domain.UserDirective{Text: "increase dialogue ratio", Chapter: 3, CreatedAt: "2026-06-11T10:00:00+08:00"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(list) != 1 || list[0].Text != "increase dialogue ratio" {
		t.Fatalf("unexpected list after add: %+v", list)
	}

	loaded, err := store.Directives.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Chapter != 3 {
		t.Errorf("unexpected loaded list: %+v", loaded)
	}
}

func TestDirectivesAddDeduplicates(t *testing.T) {
	store := NewStore(t.TempDir())

	if _, err := store.Directives.Add(domain.UserDirective{Text: "only use Chinese for titles"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	list, err := store.Directives.Add(domain.UserDirective{Text: "only use Chinese for titles"})
	if err != nil {
		t.Fatalf("Add duplicate: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("duplicate text should not be appended, got %d entries", len(list))
	}
}

func TestDirectivesRemove(t *testing.T) {
	store := NewStore(t.TempDir())

	for i := 1; i <= 3; i++ {
		if _, err := store.Directives.Add(domain.UserDirective{Text: fmt.Sprintf("Requirement %d", i)}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}

	list, err := store.Directives.Remove(2)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(list) != 2 || list[0].Text != "Requirement 1" || list[1].Text != "Requirement 3" {
		t.Errorf("unexpected list after remove: %+v", list)
	}

	if _, err := store.Directives.Remove(5); err == nil {
		t.Error("expected error for out-of-range index")
	}
	if _, err := store.Directives.Remove(0); err == nil {
		t.Error("expected error for index 0")
	}
}

func TestDirectivesAddCapacityLimit(t *testing.T) {
	store := NewStore(t.TempDir())

	for i := 1; i <= maxDirectives; i++ {
		if _, err := store.Directives.Add(domain.UserDirective{Text: fmt.Sprintf("Requirement %d", i)}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	if _, err := store.Directives.Add(domain.UserDirective{Text: "exceeded requirement"}); err == nil {
		t.Error("expected error when exceeding capacity")
	}

	// Should be able to add another after deleting one
	if _, err := store.Directives.Remove(1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := store.Directives.Add(domain.UserDirective{Text: "requirement after freeing a slot"}); err != nil {
		t.Errorf("Add after remove should succeed: %v", err)
	}
}
