package startup

import (
	"fmt"
	"strings"
)

// PrepareQuick turns direct user input into a plan ready for the Engine.
func PrepareQuick(req Request) (Plan, error) {
	prompt := strings.TrimSpace(req.UserPrompt)
	if prompt == "" {
		return Plan{}, fmt.Errorf("prompt is required")
	}
	return Plan{
		Mode:        ModeQuick,
		DisplayName: "Quick Start",
		RawPrompt:   prompt,
	}, nil
}
