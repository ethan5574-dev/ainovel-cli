package startup

import (
	"fmt"
	"os"
	"strings"
)

// LoadPromptFile reads a file as the initial creative requirements.
func LoadPromptFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read prompt: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// PrepareQuick prepares the quick-start prompt.
func PrepareQuick(rawPrompt string) (string, error) {
	prompt := strings.TrimSpace(rawPrompt)
	if prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}
	return prompt, nil
}
