package domain

// StateChange represents character/entity state change record.
type StateChange struct {
	Chapter  int    `json:"chapter"`
	Entity   string `json:"entity"`              // Character name or entity name
	Field    string `json:"field"`               // Changed attribute: realm/location/status/power/relation etc.
	OldValue string `json:"old_value,omitempty"` // Before change (empty for first occurrence)
	NewValue string `json:"new_value"`           // After change
	Reason   string `json:"reason,omitempty"`    // Reason for change
}
