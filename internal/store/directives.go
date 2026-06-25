package store

import (
	"fmt"
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// maxDirectives is the upper bound of user directives: preventing envelope dragging a long sequence of historical interventions after months of running.
// Exceeding limits will trigger coordinator to arbitrate and merge/remove old demands.
const maxDirectives = 20

// DirectivesStore manages user persistent writing instructions (meta/user_directives.json).
type DirectivesStore struct{ io *IO }

func NewDirectivesStore(io *IO) *DirectivesStore { return &DirectivesStore{io: io} }

// Load reads all persistent writing instructions. Returns empty list if file doesn't exist.
func (s *DirectivesStore) Load() ([]domain.UserDirective, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.loadUnlocked()
}

// Add appends a persistent writing instruction and returns the updated complete list.
// Text identical to existing items won't be appended (idempotent); returns error when exceeding capacity limits.
func (s *DirectivesStore) Add(d domain.UserDirective) ([]domain.UserDirective, error) {
	var list []domain.UserDirective
	err := s.io.WithWriteLock(func() error {
		existing, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		for _, e := range existing {
			if e.Text == d.Text {
				list = existing
				return nil
			}
		}
		if len(existing) >= maxDirectives {
			return fmt.Errorf("persistent instructions have reached the limit of %d, please delete using remove or merge old demands first", maxDirectives)
		}
		list = append(existing, d)
		return s.io.WriteJSONUnlocked("meta/user_directives.json", list)
	})
	if err != nil {
		return nil, err
	}
	return list, nil
}

// Remove deletes a persistent writing instruction by 1-based index and returns the updated complete list.
func (s *DirectivesStore) Remove(index int) ([]domain.UserDirective, error) {
	var list []domain.UserDirective
	err := s.io.WithWriteLock(func() error {
		existing, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if index < 1 || index > len(existing) {
			return fmt.Errorf("index %d out of bounds (currently %d items total)", index, len(existing))
		}
		list = append(existing[:index-1], existing[index:]...)
		return s.io.WriteJSONUnlocked("meta/user_directives.json", list)
	})
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (s *DirectivesStore) loadUnlocked() ([]domain.UserDirective, error) {
	var list []domain.UserDirective
	if err := s.io.ReadJSONUnlocked("meta/user_directives.json", &list); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return list, nil
}
