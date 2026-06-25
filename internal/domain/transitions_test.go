package domain

import "testing"

func TestCanTransitionPhase(t *testing.T) {
	tests := []struct {
		from Phase
		to   Phase
		want bool
	}{
		{from: "", to: PhaseInit, want: true},
		{from: PhaseInit, to: PhasePremise, want: true},
		{from: PhaseInit, to: PhaseOutline, want: true},
		{from: PhaseOutline, to: PhaseWriting, want: true},
		{from: PhaseWriting, to: PhaseComplete, want: true},
		{from: PhaseOutline, to: PhasePremise, want: false},
		{from: PhaseComplete, to: PhaseWriting, want: false},
	}
	for _, tt := range tests {
		if got := CanTransitionPhase(tt.from, tt.to); got != tt.want {
			t.Fatalf("CanTransitionPhase(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestCanTransitionFlow(t *testing.T) {
	tests := []struct {
		from FlowState
		to   FlowState
		want bool
	}{
		{from: "", to: FlowRewriting, want: true},
		{from: FlowWriting, to: FlowReviewing, want: true},
		{from: FlowReviewing, to: FlowPolishing, want: true},
		{from: FlowRewriting, to: FlowWriting, want: true},
		{from: FlowSteering, to: FlowRewriting, want: true},
		{from: FlowRewriting, to: FlowReviewing, want: false},
		{from: FlowPolishing, to: FlowReviewing, want: false},
	}
	for _, tt := range tests {
		if got := CanTransitionFlow(tt.from, tt.to); got != tt.want {
			t.Fatalf("CanTransitionFlow(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}

func TestExtractNovelNameFromPremise_Placeholder(t *testing.T) {
	cases := []struct {
		name    string
		premise string
		want    string
	}{
		{"Real novel name", "# Long Night Approaching Dawn\n\n## Genre", "Long Night Approaching Dawn"},
		{"With book quotes", "# 《Astral Shore》\n## Genre", "Astral Shore"},
		{"Placeholder - Book Title", "# Book Title\n## Genre", ""},
		{"Placeholder - Example Title", "# 《Example Title》\n## Genre", ""},
		{"Placeholder - Actual Title", "# Actual Title\n## Genre", ""},
		{"First line not title", "plain text first line\n# Book Title", ""},
	}
	for _, c := range cases {
		if got := ExtractNovelNameFromPremise(c.premise); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
