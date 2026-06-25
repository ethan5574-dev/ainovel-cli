// Package models provides the LLM model metadata registry (context window, max output tokens, pricing).
// Data source is the OpenRouter API, with a compile-time baseline + runtime refresh.
package models

//go:generate go run gen_models.go

import (
	"strings"
	"sync"
)

// ModelEntry describes a known LLM model.
type ModelEntry struct {
	Provider            string  `json:"provider"`               // OpenRouter normalized provider name (anthropic/openai/gemini/...)
	ID                  string  `json:"id"`                     // Model ID (excluding provider prefix)
	Name                string  `json:"name"`                   // Display name
	ContextWindow       int     `json:"context_window"`         // Input context window size
	MaxTokens           int     `json:"max_tokens"`             // Max output tokens limit
	InputCostPer1M      float64 `json:"input_cost_per_1m"`      // Input price (USD/1M tokens)
	OutputCostPer1M     float64 `json:"output_cost_per_1m"`     // Output price (USD/1M tokens)
	CacheReadCostPer1M  float64 `json:"cache_read_cost_per_1m"` // Cache read price (USD/1M tokens)
	CacheWriteCostPer1M float64 `json:"cache_write_cost_per_1m"`
}

// ModelRegistry stores known models, supporting fuzzy resolution and runtime merging.
type ModelRegistry struct {
	mu     sync.RWMutex
	models []ModelEntry
}

// NewModelRegistry returns a registry loaded with the compile-time baseline models.
func NewModelRegistry() *ModelRegistry {
	r := &ModelRegistry{}
	r.models = append(r.models, generatedModels...)
	return r
}

var (
	defaultRegistry     *ModelRegistry
	defaultRegistryOnce sync.Once
)

// DefaultRegistry returns the global registry (lazy loaded, thread safe).
// Call StartPricingRefresh during startup to trigger a background price/window refresh.
func DefaultRegistry() *ModelRegistry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewModelRegistry()
	})
	return defaultRegistry
}

// Resolve finds an entry by a model identifier (which can be "provider/model", full ID, or a partial name).
//
// Match order:
//  1. If it contains "/", perform an exact match by "provider/model"
//  2. Exact match or date-suffix match
//  3. Substring match (where ID or Name contains pattern)
//
// When multiple matches are found, it prioritizes returning the alias without a date suffix (e.g. claude-sonnet-4 over claude-sonnet-4-20250514).
func (r *ModelRegistry) Resolve(pattern string) (*ModelEntry, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	if idx := strings.Index(pattern, "/"); idx > 0 {
		prov := pattern[:idx]
		modelID := pattern[idx+1:]
		if entry, ok := lookupModelEntry(r.models, prov, modelID); ok {
			return &entry, true
		}
		// The vendor prefix used by OpenRouter (e.g. google/, x-ai/) does not always match the local Provider name.
		// Fall back to querying by model ID only, ensuring e.g. "google/gemini-2.5-pro" matches the gemini entry.
		if entry, ok := lookupModelEntry(r.models, "", modelID); ok {
			return &entry, true
		}
	}

	if entry, ok := lookupModelEntry(r.models, "", pattern); ok {
		return &entry, true
	}

	lower := strings.ToLower(pattern)
	normalized := normalizeModelLookupID(pattern)
	var candidates []int
	for i := range r.models {
		if strings.Contains(normalizeModelLookupID(r.models[i].ID), normalized) ||
			strings.Contains(strings.ToLower(r.models[i].ID), lower) ||
			strings.Contains(strings.ToLower(r.models[i].Name), lower) {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}

	best := candidates[0]
	for _, i := range candidates[1:] {
		if !hasDatedSuffix(r.models[i].ID) && hasDatedSuffix(r.models[best].ID) {
			best = i
		}
	}
	entry := r.models[best]
	return &entry, true
}

// ResolveContextWindow returns the context window of a given model; returns 0 if not found.
func (r *ModelRegistry) ResolveContextWindow(pattern string) int {
	if e, ok := r.Resolve(pattern); ok {
		return e.ContextWindow
	}
	return 0
}

// List returns all models (optional filter, empty string returns all).
func (r *ModelRegistry) List(filter string) []ModelEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if filter == "" {
		return append([]ModelEntry{}, r.models...)
	}
	lower := strings.ToLower(filter)
	normalized := normalizeModelLookupID(filter)
	var out []ModelEntry
	for _, m := range r.models {
		if strings.Contains(strings.ToLower(m.Provider), lower) ||
			strings.Contains(normalizeModelLookupID(m.ID), normalized) ||
			strings.Contains(strings.ToLower(m.ID), lower) ||
			strings.Contains(strings.ToLower(m.Name), lower) {
			out = append(out, m)
		}
	}
	return out
}

// MergeModels merges fetched models case-insensitively by provider+id.
// Non-zero price/window/MaxTokens/Name will overwrite existing entries; new entries are appended.
func (r *ModelRegistry) MergeModels(fetched []ModelEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := make(map[string]int, len(r.models))
	for i, m := range r.models {
		idx[strings.ToLower(m.Provider+"/"+m.ID)] = i
	}
	for _, f := range fetched {
		key := strings.ToLower(f.Provider + "/" + f.ID)
		if i, ok := idx[key]; ok {
			if f.InputCostPer1M > 0 || f.OutputCostPer1M > 0 {
				r.models[i].InputCostPer1M = f.InputCostPer1M
				r.models[i].OutputCostPer1M = f.OutputCostPer1M
				r.models[i].CacheReadCostPer1M = f.CacheReadCostPer1M
				r.models[i].CacheWriteCostPer1M = f.CacheWriteCostPer1M
			}
			if f.ContextWindow > 0 {
				r.models[i].ContextWindow = f.ContextWindow
			}
			if f.MaxTokens > 0 {
				r.models[i].MaxTokens = f.MaxTokens
			}
			if f.Name != "" {
				r.models[i].Name = f.Name
			}
		} else {
			r.models = append(r.models, f)
			idx[key] = len(r.models) - 1
		}
	}
}
