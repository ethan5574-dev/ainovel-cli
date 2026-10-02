package store

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// BuildCast derives the current supporting-cast view from accepted records.
func (s *Store) BuildCast(chapters []int) ([]domain.CastEntry, error) {
	records, err := s.ChapterRecords.LoadCompleted(chapters)
	if err != nil {
		return nil, fmt.Errorf("read chapter records: %w", err)
	}
	characters, err := s.Characters.Load()
	if err != nil {
		return nil, fmt.Errorf("read core characters: %w", err)
	}
	return domain.ProjectCast(records, characters), nil
}
