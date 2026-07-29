package domain

import "time"

// UsageSchemaVersion is the compatible version number for meta/usage.json.
// If AgentUsageTotals field semantics change in the future, increment this value; UsageStore.Load should ignore mismatched versions and trigger replay reconstruction.
const UsageSchemaVersion = 2

// UsageState is a persistable snapshot of cumulative token/cost usage.
// Maintained in memory by UsageTracker, debounced and written to meta/usage.json periodically.
//
// Note: Sliding window samples ("recent N hit rate") in UsageTracker are NOT persisted —
// they only serve short-term UI diagnostics, and will recover their semantics after accumulating a few rounds post-restart.
// MissingAssistantUsage is persisted, providing higher diagnostic value across restarts.
type UsageState struct {
	Schema       int                         `json:"schema"`
	UpdatedAt    time.Time                   `json:"updated_at"`
	Overall      AgentUsageTotals            `json:"overall"`
	PerAgent     map[string]AgentUsageTotals `json:"per_agent"`
	PerModel     map[string]AgentUsageTotals `json:"per_model,omitempty"`
	MissingUsage int                         `json:"missing_assistant_usage"`
}

// AgentUsageTotals is the persistable format of cumulative usage counters for a single agent (or overall).
type AgentUsageTotals struct {
	Input        int     `json:"input"`
	Output       int     `json:"output"`
	CacheRead    int     `json:"cache_read"`
	CacheWrite   int     `json:"cache_write"`
	Cost         float64 `json:"cost_usd"`
	Saved        float64 `json:"saved_usd"`
	CacheCapable bool    `json:"cache_capable"`
	// CacheBreaks 是 live 检测到的缓存链断裂次数（前缀未缩短而命中骤降）。
	// 只在实时路径累计，session replay 不重放检测。
	CacheBreaks int `json:"cache_breaks,omitempty"`
}
