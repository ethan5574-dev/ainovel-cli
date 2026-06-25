package bootstrap

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// DefaultContextWindow is the fallback window size when a model is not registered.
const DefaultContextWindow = 200000

// CompactRatio is the relative threshold that triggers context compaction: compact when tokens >= window * CompactRatio.
// 0.85 is an empirical value that leaves 15% headroom for the next prompt and large tool outputs, while still
// compacting large-window models before they are completely full.
//
// This is not user-configurable: in a multi-model setup, a fixed sane default is more reliable than another knob.
const CompactRatio = 0.85

// MinCompactReserve is the lower bound for ReserveTokens. Small-window models can otherwise reserve too little,
// causing a compact-and-immediately-overrun loop. 8000 keeps enough buffer for the worst case.
const MinCompactReserve = 8000

// CompactReserveTokens derives ReserveTokens from CompactRatio and applies the MinCompactReserve floor:
//
//	threshold = window - reserve = window * CompactRatio
//	reserve   = max(MinCompactReserve, window * (1 - CompactRatio))
//
// Used for agentcore.context.Engine's EngineConfig.ReserveTokens.
func CompactReserveTokens(window int) int {
	if window <= 0 {
		return 0
	}
	reserve := window - int(float64(window)*CompactRatio)
	if reserve < MinCompactReserve {
		return MinCompactReserve
	}
	return reserve
}

// ProviderConfig defines credentials for a single LLM provider.
type ProviderConfig struct {
	Type    string   `json:"type,omitempty"`     // API protocol type (openai/anthropic/gemini), used for custom proxies
	APIKey  string   `json:"api_key,omitempty"`  // API Key
	BaseURL string   `json:"base_url,omitempty"` // API Base URL
	Models  []string `json:"models,omitempty"`   // Optional model list shown in the TUI selector
	// ExtraBody passes extra request parameters through to the provider (e.g. temperature/top_p/min_p/presence_penalty
	// or vendor-specific keys such as NVIDIA chat_template_kwargs). For OpenAI-compatible providers, these are merged
	// directly into the request body; users are responsible for the values they supply.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// Extra passes through provider-level settings (litellm.ProviderConfig.Extra) for transport/client options such as
	// HTTP headers, user_agent, and anthropic_beta.
	Extra map[string]any `json:"extra,omitempty"`
}

// RequiresAPIKey reports whether the provider must explicitly configure api_key.
// Rules:
// 1. ollama / bedrock may omit a key;
// 2. configs with an explicit Type are treated as custom proxies and may omit a key;
// 3. all other providers require a key by default.
func (pc ProviderConfig) RequiresAPIKey(name string) bool {
	switch name {
	case "ollama", "bedrock":
		return false
	}
	return pc.Type == ""
}

// ProviderType returns the effective API protocol type.
// Explicit Type wins; otherwise the provider name must exist in the litellm registry.
func (pc ProviderConfig) ProviderType(name string) (string, error) {
	if pc.Type != "" {
		return pc.Type, nil
	}
	if llm.IsProviderRegistered(name) {
		return name, nil
	}
	return "", fmt.Errorf("provider %q is missing type and is not in the litellm known provider list: %w", name, errs.ErrConfig)
}

// ModelRef represents a provider/model pair.
type ModelRef struct {
	Provider string `json:"provider"` // Provider name (key in Providers map)
	Model    string `json:"model"`    // Model name (passed through as-is)
}

// RoleConfig defines a per-role model override.
type RoleConfig struct {
	Provider  string     `json:"provider"`            // Main provider name (key in Providers map)
	Model     string     `json:"model"`               // Main model name (passed through as-is)
	Fallbacks []ModelRef `json:"fallbacks,omitempty"` // Explicit fallback provider/model list
	// Thinking is this role's thinking strength (off/minimal/low/medium/high/xhigh); empty inherits the top-level default.
	// It is validated by agents.ParseThinkingLevel; out-of-range values are treated as empty.
	Thinking string `json:"thinking,omitempty"`
}

// knownRoles lists supported role names.
var knownRoles = map[string]bool{
	"coordinator": true,
	"architect":   true,
	"writer":      true,
	"editor":      true,
}

// Config is the application configuration.
type Config struct {
	// Runtime-only field (not serialized to JSON)
	OutputDir string `json:"-"` // Output root directory

	// Default LLM config
	Provider  string `json:"provider"` // Default provider (key in Providers map)
	ModelName string `json:"model"`    // Default model name
	// Thinking is the top-level default thinking level (off/minimal/low/medium/high/xhigh).
	// Empty means no override and keeps the model/provider default.
	Thinking string `json:"thinking,omitempty"`

	// Provider credentials
	Providers map[string]ProviderConfig `json:"providers,omitempty"`

	// Role-level model overrides
	Roles map[string]RoleConfig `json:"roles,omitempty"`

	// Creative style
	Style string `json:"style,omitempty"`

	// ContextWindow controls the window size used for context compaction. When left empty (0), it is resolved from
	// the model name: registered models use their real window, and unknown models fall back to DefaultContextWindow.
	// An explicit value takes priority and can be used to pin a custom model or force earlier compaction.
	// It only affects compaction thresholds, not the actual LLM request length.
	ContextWindow int `json:"context_window,omitempty"`

	// Budget is the per-book cost budget policy; it is enabled when book_usd > 0.
	Budget BudgetConfig `json:"budget,omitzero"`

	// Notify is the unattended alert configuration; enabled by default (system channel fallback).
	Notify NotifyConfig `json:"notify,omitzero"`
}

// BudgetConfig is the user's policy declaration for a single book budget.
// Crossing the limit stops the run; Host only enforces it.
type BudgetConfig struct {
	BookUSD   float64 `json:"book_usd,omitempty"`   // Enables only when set; 0/default = unlimited
	WarnRatio float64 `json:"warn_ratio,omitempty"` // Warning threshold, default 0.8
	HardStop  bool    `json:"hard_stop,omitempty"`  // true = stop immediately on limit; default waits for the current subagent task to finish
}

// Enabled reports whether the budget policy is enabled.
func (b BudgetConfig) Enabled() bool { return b.BookUSD > 0 }

// NotifyConfig configures unattended alert channels.
type NotifyConfig struct {
	Enabled *bool    `json:"enabled,omitempty"` // Default true (system channel works with zero config)
	Command string   `json:"command,omitempty"` // Optional; replaces system channel when set
	Events  []string `json:"events,omitempty"`  // Optional; filter kinds (run_end/repeat/budget), default all
}

// IsEnabled reports whether alerts are enabled (default true).
func (n NotifyConfig) IsEnabled() bool { return n.Enabled == nil || *n.Enabled }

// ValidateBase validates the base configuration.
func (c *Config) ValidateBase() error {
	if err := validateConfigText("provider", c.Provider); err != nil {
		return err
	}
	if err := validateConfigText("model", c.ModelName); err != nil {
		return err
	}

	if c.Provider == "" {
		return fmt.Errorf("provider is required: %w", errs.ErrConfig)
	}
	if c.ModelName == "" {
		return fmt.Errorf("model is required: %w", errs.ErrConfig)
	}

	// Default provider must have credentials
	pc, ok := c.Providers[c.Provider]
	if !ok {
		return fmt.Errorf("provider %q has no credentials configured in providers; if you override provider in ./.ainovel/config.json, you must also declare providers.%s (including api_key/base_url), not just change the top-level provider: %w", c.Provider, c.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(c.Provider) && pc.APIKey == "" {
		return fmt.Errorf("provider %q has no api_key configured: %w", c.Provider, errs.ErrConfig)
	}
	if err := validateProviderConfigText(c.Provider, pc); err != nil {
		return err
	}
	for name, provider := range c.Providers {
		if err := validateConfigText("provider name", name); err != nil {
			return err
		}
		if err := validateProviderConfigText(name, provider); err != nil {
			return err
		}
	}

	// Validate role overrides
	for role, rc := range c.Roles {
		if err := validateConfigText("role name", role); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("role %q provider", role), rc.Provider); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("role %q model", role), rc.Model); err != nil {
			return err
		}
		if !knownRoles[role] {
			return fmt.Errorf("unknown role %q in roles config (valid: coordinator/architect/writer/editor): %w", role, errs.ErrConfig)
		}
		if rc.Provider == "" || rc.Model == "" {
			return fmt.Errorf("role %q must have both provider and model: %w", role, errs.ErrConfig)
		}
		if err := c.validateModelRef(
			fmt.Sprintf("role %q", role),
			ModelRef{Provider: rc.Provider, Model: rc.Model},
		); err != nil {
			return err
		}
		for i, fallback := range rc.Fallbacks {
			if err := validateConfigText(fmt.Sprintf("role %q fallback[%d] provider", role, i), fallback.Provider); err != nil {
				return err
			}
			if err := validateConfigText(fmt.Sprintf("role %q fallback[%d] model", role, i), fallback.Model); err != nil {
				return err
			}
			if err := c.validateModelRef(
				fmt.Sprintf("role %q fallback[%d]", role, i),
				fallback,
			); err != nil {
				return err
			}
		}
	}

	// Validate budget policy
	if c.Budget.BookUSD < 0 {
		return fmt.Errorf("budget.book_usd must be >= 0: %w", errs.ErrConfig)
	}
	if c.Budget.Enabled() && (c.Budget.WarnRatio <= 0 || c.Budget.WarnRatio >= 1) {
		return fmt.Errorf("budget.warn_ratio must be in (0, 1): %w", errs.ErrConfig)
	}

	// Validate alert config
	if err := validateConfigText("notify.command", c.Notify.Command); err != nil {
		return err
	}
	for _, ev := range c.Notify.Events {
		if !knownNotifyEvents[ev] {
			return fmt.Errorf("unknown notify event %q (valid: run_end/repeat/budget): %w", ev, errs.ErrConfig)
		}
	}

	return nil
}

var knownNotifyEvents = map[string]bool{"run_end": true, "repeat": true, "budget": true}

func validateProviderConfigText(name string, pc ProviderConfig) error {
	fields := []struct {
		label string
		value string
	}{
		{label: fmt.Sprintf("provider %q type", name), value: pc.Type},
		{label: fmt.Sprintf("provider %q api_key", name), value: pc.APIKey},
		{label: fmt.Sprintf("provider %q base_url", name), value: pc.BaseURL},
	}
	for _, field := range fields {
		if err := validateConfigText(field.label, field.value); err != nil {
			return err
		}
	}
	for i, model := range pc.Models {
		if err := validateConfigText(fmt.Sprintf("provider %q models[%d]", name, i), model); err != nil {
			return err
		}
	}
	return nil
}

func validateConfigText(name, value string) error {
	if utils.ContainsControl(value) {
		return fmt.Errorf("%s contains control character: %w", name, errs.ErrConfig)
	}
	return nil
}

// DefaultProviderConfig returns the credential config for the default provider.
func (c *Config) DefaultProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	return c.Providers[c.Provider]
}

// FillDefaults fills default values.
func (c *Config) FillDefaults() {
	if c.OutputDir == "" {
		c.OutputDir = filepath.Join("output", "novel")
	}
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	if c.Roles == nil {
		c.Roles = make(map[string]RoleConfig)
	}
	if c.Style == "" {
		c.Style = "default"
	}
	if c.Budget.Enabled() && c.Budget.WarnRatio == 0 {
		c.Budget.WarnRatio = 0.8
	}
}

// ContextWindowSource marks the source of a context-window value for logs and diagnostics.
type ContextWindowSource string

const (
	CtxWindowConfig   ContextWindowSource = "config"   // Explicit config file context_window
	CtxWindowRegistry ContextWindowSource = "registry" // Registry hit
	CtxWindowDefault  ContextWindowSource = "default"  // Fallback (custom proxy / unknown model)
)

// ResolveContextWindow resolves the effective window used for context compaction, in priority order:
//  1. Config ContextWindow > 0 -> use it directly
//  2. Lookup by model name in models.DefaultRegistry
//  3. Fallback to DefaultContextWindow
//
// The result only affects compaction thresholds, not the actual LLM request length.
func (c Config) ResolveContextWindow(modelName string) (int, ContextWindowSource) {
	if c.ContextWindow > 0 {
		return c.ContextWindow, CtxWindowConfig
	}
	if rw := models.DefaultRegistry().ResolveContextWindow(modelName); rw > 0 {
		return rw, CtxWindowRegistry
	}
	return DefaultContextWindow, CtxWindowDefault
}

// ResolveThinking returns the effective raw thinking level for a role (off/minimal/low/medium/high/xhigh or empty).
// Priority: role-level Roles[role].Thinking -> top-level Thinking -> "" (no override, keep model/provider default).
// Empty role or "default" uses the top-level default.
func (c Config) ResolveThinking(role string) string {
	if role != "" && role != "default" {
		if rc, ok := c.Roles[role]; ok && rc.Thinking != "" {
			return rc.Thinking
		}
	}
	return c.Thinking
}

// LogContextWindowChoice logs the chosen window for a role.
// When source=default, it warns that the model did not hit the registry and compaction will use the fallback window.
func LogContextWindowChoice(role, model string, window int, source ContextWindowSource) {
	attrs := []any{"module", "context", "role", role, "model", model, "window", window, "source", source}
	switch source {
	case CtxWindowDefault:
		slog.Warn("unrecognized model, using fallback window (custom proxy or not in registry; set context_window explicitly if needed)", attrs...)
	case CtxWindowConfig:
		slog.Info("context window (from config file context_window)", attrs...)
	default:
		slog.Info("context window", attrs...)
	}
}

// CandidateModels returns the models available for switching under a provider.
// Prefer the provider's declared models and supplement them with any models already present in config.
func (c Config) CandidateModels(provider string) []string {
	if provider == "" {
		return nil
	}

	seen := make(map[string]bool)
	models := make([]string, 0, 4)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		models = append(models, model)
	}

	if pc, ok := c.Providers[provider]; ok {
		for _, model := range pc.Models {
			add(model)
		}
	}
	if c.Provider == provider {
		add(c.ModelName)
	}
	for _, rc := range c.Roles {
		if rc.Provider == provider {
			add(rc.Model)
		}
		for _, fallback := range rc.Fallbacks {
			if fallback.Provider == provider {
				add(fallback.Model)
			}
		}
	}
	return models
}

func (c Config) validateModelRef(owner string, ref ModelRef) error {
	if ref.Provider == "" || ref.Model == "" {
		return fmt.Errorf("%s must have both provider and model: %w", owner, errs.ErrConfig)
	}

	pc, ok := c.Providers[ref.Provider]
	if !ok {
		return fmt.Errorf("%s references provider %q which is not configured: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(ref.Provider) && pc.APIKey == "" {
		return fmt.Errorf("%s references provider %q which has no api_key: %w", owner, ref.Provider, errs.ErrConfig)
	}
	return nil
}
