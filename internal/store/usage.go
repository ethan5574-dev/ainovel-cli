package store

import (
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// UsageStore persists cumulative token/cost usage to meta/usage.json.
// Writes use atomic write via IO (tmp + rename), where Save completely overwrites the entire state each time.
type UsageStore struct{ io *IO }

func NewUsageStore(io *IO) *UsageStore { return &UsageStore{io: io} }

// Load reads usage.json. If the file does not exist or the schema version does not match, it returns (nil, nil),
// allowing the caller to decide whether to perform a session replay for one-time backfilling.
func (s *UsageStore) Load() (*domain.UsageState, error) {
	var state domain.UsageState
	if err := s.io.ReadJSON("meta/usage.json", &state); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if state.Schema != domain.UsageSchemaVersion {
		return nil, nil
	}
	return &state, nil
}

// Save completely overwrites the state on disk. The caller is responsible for debounce/throttling.
func (s *UsageStore) Save(state domain.UsageState) error {
	state.Schema = domain.UsageSchemaVersion
	return s.io.WriteJSON("meta/usage.json", state)
}
