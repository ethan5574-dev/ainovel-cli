package bootstrap

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// DefaultContextWindow is the fallback window size for models absent from the registry.
const DefaultContextWindow = 200000

// CompactRatio is the relative threshold for context compaction: compact when tokens >= window * CompactRatio.
// 0.85 is an empirical value that leaves 15% headroom for the next prompt and large tool results,
// while compacting large-window models before they are completely full.
//
// The ratio is not user-configurable; users only configure each model's actual context_window.
const CompactRatio = 0.85

// MinCompactReserve is the lower bound for ReserveTokens. Small-window models such as local qwen3:8b
// would otherwise reserve only 4800 tokens at a 0.15 ratio, causing a compact-and-immediately-overrun loop.
// The 8000-token floor preserves enough buffer for the worst case.
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
	Type    string        `json:"type,omitempty"`     // API protocol type (openai/anthropic/gemini), used for custom proxies
	API     string        `json:"api,omitempty"`      // OpenAI protocol endpoint: chat (default) / responses
	APIKey  string        `json:"api_key,omitempty"`  // API Key
	BaseURL string        `json:"base_url,omitempty"` // API Base URL
	Models  []ModelConfig `json:"models,omitempty"`   // Optional model list shown in the TUI selector
	// ExtraBody passes extra parameters through on every request (for example temperature/top_p/min_p,
	// presence_penalty, or vendor-specific keys such as NVIDIA chat_template_kwargs).
	// OpenAI-compatible providers merge these values into the request body verbatim.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// Extra passes provider-level settings (litellm.ProviderConfig.Extra) such as HTTP headers,
	// user_agent, and anthropic_beta to the client/transport layer.
	Extra map[string]any `json:"extra,omitempty"`
	// StreamIdleTimeout is the streaming idle watchdog: the stream is interrupted when no chunk
	// arrives within this duration (Go duration syntax such as "900s" or "15m"). Empty means 5m.
	// Slow self-hosted inference on LocalAI/ollama can increase this per provider without affecting others.
	StreamIdleTimeout string `json:"stream_idle_timeout,omitempty"`
}

// ModelConfig describes a selectable model under a provider and its optional context window.
// For backward compatibility it accepts both JSON strings ("model-name") and objects;
// writes always normalize entries to object form.
type ModelConfig struct {
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window,omitempty"`
	// JSONSchema is a tri-state declaration for native structured output
	// (response_format json_schema): unset uses adapter capability detection; true declares support
	// and exposes request rejection; false forces the prompt contract. Custom proxies and gateways
	// rely on the user's declaration; the program does not probe them.
	JSONSchema *bool `json:"json_schema,omitempty"`
}

func (m *ModelConfig) UnmarshalJSON(data []byte) error {
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		m.Name = legacy
		m.ContextWindow = 0
		m.JSONSchema = nil
		return nil
	}
	type modelConfigAlias ModelConfig
	var decoded modelConfigAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("model config must be a string or object: %w", err)
	}
	*m = ModelConfig(decoded)
	return nil
}

// ModelConfig returns the explicit configuration for a named model.
func (pc ProviderConfig) ModelConfig(name string) (ModelConfig, bool) {
	name = strings.TrimSpace(name)
	for _, model := range pc.Models {
		if strings.TrimSpace(model.Name) == name {
			return model, true
		}
	}
	return ModelConfig{}, false
}

// ModelJSONSchema returns the model's tri-state json_schema declaration;
// it returns nil when the model is not listed or has no override.
func (c Config) ModelJSONSchema(provider, model string) *bool {
	if pc, ok := c.Providers[provider]; ok {
		if mc, ok := pc.ModelConfig(model); ok {
			return mc.JSONSchema
		}
	}
	return nil
}

// defaultStreamIdleTimeout covers long outputs and long-context reasoning providers such as
// mimo and deepseek-r1, whose servers may emit no reasoning delta during the thinking phase.
// litellm's two-minute watchdog frequently kills 8000-word chapters; five minutes covers most cases.
const defaultStreamIdleTimeout = 5 * time.Minute

// StreamIdleTimeoutValue parses the provider's streaming idle timeout; empty falls back to the default.
func (pc ProviderConfig) StreamIdleTimeoutValue() (time.Duration, error) {
	s := strings.TrimSpace(pc.StreamIdleTimeout)
	if s == "" {
		return defaultStreamIdleTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use Go duration like \"900s\" / \"15m\")", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive, got %q", s)
	}
	return d, nil
}

// RequiresAPIKey reports whether the provider must explicitly configure api_key.
// ollama and bedrock may omit a key; an explicit Type marks a custom proxy and may omit one;
// other providers require a key by default.
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
	return "", fmt.Errorf("provider %q is missing type and is not in the litellm provider registry: %w", name, errs.ErrConfig)
}

// ModelRef represents a provider/model pair.
type ModelRef struct {
	Provider string `json:"provider"` // Provider name (key in the Providers map).
	Model    string `json:"model"`    // Model name, passed through unchanged.
}

// RoleConfig defines a per-role model override.
type RoleConfig struct {
	Provider  string     `json:"provider"`            // Primary provider name (key in the Providers map).
	Model     string     `json:"model"`               // Primary model name, passed through unchanged.
	Fallbacks []ModelRef `json:"fallbacks,omitempty"` // Explicit fallback provider/model list.
	// ReasoningEffort is this role's reasoning level (off/low/medium/high/xhigh/max).
	// Empty inherits the top-level default and is validated by agents.ParseThinkingLevel.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// knownRoles lists configurable roles. Arbiter currently has no role-level override
// and always uses the top-level default model. import_* roles select model tiers for
// semantic import functions; they fall back to architect when unset.
var knownRoles = map[string]bool{
	"architect":         true,
	"writer":            true,
	"editor":            true,
	"import_segment":    true,
	"import_analyze":    true,
	"import_synthesize": true,
}

// Config is the novel application's configuration.
type Config struct {
	// Runtime fields (not serialized to JSON).
	OutputDir string `json:"-"` // Output root directory.

	// Default LLM configuration.
	Provider  string `json:"provider"` // Default provider (key in the Providers map).
	ModelName string `json:"model"`    // Default model name.
	// ReasoningEffort is the top-level reasoning level. Empty leaves the model/provider default unchanged.
	// Roles without their own reasoning_effort inherit this value.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// Provider credential store.
	Providers map[string]ProviderConfig `json:"providers,omitempty"`

	// Per-role model overrides.
	Roles map[string]RoleConfig `json:"roles,omitempty"`

	// Writing parameters.
	Style string `json:"style,omitempty"`

	// ContextWindow is the legacy global context window, retained as a fallback after
	// model-specific context_window values. It only affects compaction thresholds.
	ContextWindow int `json:"context_window,omitempty"`

	// Budget policy for one book; enabled only when book_usd > 0.
	Budget BudgetConfig `json:"budget,omitzero"`

	// Notify configuration for unattended alerts; enabled by default with the system channel as fallback.
	Notify NotifyConfig `json:"notify,omitzero"`

	// DisableUpdateCheck 关闭启动时的新版本检查提醒（默认开）。检查只读
	// GitHub Releases 公开接口，结果缓存在本地配置目录，不上报任何数据。
	DisableUpdateCheck bool `json:"disable_update_check,omitempty"`
}

// BudgetConfig declares the user's wallet policy for one book. Stopping at the limit is
// equivalent to a manual Abort; Host only executes it and does not evaluate model behavior.
type BudgetConfig struct {
	BookUSD   float64 `json:"book_usd,omitempty"`   // Required to enable; 0/omitted means unlimited.
	WarnRatio float64 `json:"warn_ratio,omitempty"` // Warning threshold, default 0.8.
	HardStop  bool    `json:"hard_stop,omitempty"`  // Stop immediately at the limit; otherwise wait for the current worker.
}

// Enabled reports whether the budget policy is enabled.
func (b BudgetConfig) Enabled() bool { return b.BookUSD > 0 }

// NotifyConfig configures the unattended alert channel.
type NotifyConfig struct {
	Enabled *bool    `json:"enabled,omitempty"` // Defaults to true; the system channel needs no configuration.
	Command string   `json:"command,omitempty"` // Optional custom command replacing the system channel.
	Events  []string `json:"events,omitempty"`  // Optional notify.Kinds filter; all events by default.
}

// IsEnabled reports whether alerts are enabled (true by default).
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

	// The default provider must have credentials.
	pc, ok := c.Providers[c.Provider]
	if !ok {
		return fmt.Errorf("provider %q has no credentials in providers; if ./.ainovel/config.json overrides provider, it must also declare providers.%s with api_key/base_url: %w", c.Provider, c.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(c.Provider) && pc.APIKey == "" {
		return fmt.Errorf("provider %q has no api_key configured: %w", c.Provider, errs.ErrConfig)
	}
	if err := validateProviderConfigText(c.Provider, pc); err != nil {
		return err
	}
	if err := c.validateProviderAPI("default", c.Provider, pc); err != nil {
		return err
	}
	for name, provider := range c.Providers {
		if err := validateConfigText("provider name", name); err != nil {
			return err
		}
		if err := validateProviderConfigText(name, provider); err != nil {
			return err
		}
		if err := c.validateProviderAPI(fmt.Sprintf("provider %q", name), name, provider); err != nil {
			return err
		}
	}

	// Validate role overrides.
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
			return fmt.Errorf("unknown role %q in roles config (valid: architect/writer/editor/import_segment/import_analyze/import_synthesize): %w", role, errs.ErrConfig)
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

	// Validate the budget policy.
	if c.Budget.BookUSD < 0 {
		return fmt.Errorf("budget.book_usd must be >= 0: %w", errs.ErrConfig)
	}
	if c.Budget.Enabled() && (c.Budget.WarnRatio <= 0 || c.Budget.WarnRatio >= 1) {
		return fmt.Errorf("budget.warn_ratio must be in (0, 1): %w", errs.ErrConfig)
	}

	// Validate alert configuration.
	if err := validateConfigText("notify.command", c.Notify.Command); err != nil {
		return err
	}
	for _, ev := range c.Notify.Events {
		if !notify.IsKnownKind(ev) {
			return fmt.Errorf("unknown notify event %q (valid: %s): %w", ev, strings.Join(notify.Kinds(), "/"), errs.ErrConfig)
		}
	}

	return nil
}

func validateProviderConfigText(name string, pc ProviderConfig) error {
	fields := []struct {
		label string
		value string
	}{
		{label: fmt.Sprintf("provider %q type", name), value: pc.Type},
		{label: fmt.Sprintf("provider %q api", name), value: pc.API},
		{label: fmt.Sprintf("provider %q api_key", name), value: pc.APIKey},
		{label: fmt.Sprintf("provider %q base_url", name), value: pc.BaseURL},
	}
	for _, field := range fields {
		if err := validateConfigText(field.label, field.value); err != nil {
			return err
		}
	}
	seenModels := make(map[string]bool, len(pc.Models))
	for i, model := range pc.Models {
		modelName := strings.TrimSpace(model.Name)
		if err := validateConfigText(fmt.Sprintf("provider %q models[%d].name", name, i), model.Name); err != nil {
			return err
		}
		if modelName == "" {
			return fmt.Errorf("provider %q models[%d].name is required: %w", name, i, errs.ErrConfig)
		}
		if seenModels[modelName] {
			return fmt.Errorf("provider %q has duplicate model %q: %w", name, modelName, errs.ErrConfig)
		}
		seenModels[modelName] = true
		if model.ContextWindow < 0 {
			return fmt.Errorf("provider %q model %q context_window must be >= 0: %w", name, modelName, errs.ErrConfig)
		}
	}
	switch pc.API {
	case "", "chat", "responses":
	default:
		return fmt.Errorf("provider %q api must be chat or responses: %w", name, errs.ErrConfig)
	}
	if _, err := pc.StreamIdleTimeoutValue(); err != nil {
		return fmt.Errorf("provider %q stream_idle_timeout: %w: %w", name, err, errs.ErrConfig)
	}
	return nil
}

func validateConfigText(name, value string) error {
	if utils.ContainsControl(value) {
		return fmt.Errorf("%s contains control character: %w", name, errs.ErrConfig)
	}
	return nil
}

// DefaultProviderConfig returns the default provider's credential configuration.
func (c *Config) DefaultProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	return c.Providers[c.Provider]
}

// FillDefaults populates default values.
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

// ContextWindowSource identifies where a context-window value came from.
type ContextWindowSource string

const (
	CtxWindowModelConfig ContextWindowSource = "model_config" // Explicit provider model entry.
	CtxWindowConfig      ContextWindowSource = "config"       // Explicit legacy top-level context_window.
	CtxWindowRegistry    ContextWindowSource = "registry"     // OpenRouter registry hit.
	CtxWindowDefault     ContextWindowSource = "default"      // Fallback for custom/unknown models.
)

// ResolveContextWindow resolves the effective context window used for compaction, in priority order:
//  1. providers.<provider>.models[].context_window
//  2. Legacy top-level ContextWindow.
//  3. models.DefaultRegistry lookup by model name.
//  4. DefaultContextWindow for custom or unknown models.
//
// The result only controls compaction thresholds; it does not reduce the LLM API request limit.
func (c Config) ResolveContextWindow(provider, modelName string) (int, ContextWindowSource) {
	if pc, ok := c.Providers[strings.TrimSpace(provider)]; ok {
		if model, found := pc.ModelConfig(modelName); found && model.ContextWindow > 0 {
			return model.ContextWindow, CtxWindowModelConfig
		}
	}
	if c.ContextWindow > 0 {
		return c.ContextWindow, CtxWindowConfig
	}
	if rw := models.DefaultRegistry().ResolveContextWindow(modelName); rw > 0 {
		return rw, CtxWindowRegistry
	}
	return DefaultContextWindow, CtxWindowDefault
}

// ResolveReasoningEffort returns the raw effective reasoning level for a role.
// Priority is role-level ReasoningEffort, then the top-level default, then empty.
// An empty or "default" role uses the top-level value; agents.ParseThinkingLevel validates it.
func (c Config) ResolveReasoningEffort(role string) string {
	if role != "" && role != "default" {
		if rc, ok := c.Roles[role]; ok && rc.ReasoningEffort != "" {
			return rc.ReasoningEffort
		}
	}
	return c.ReasoningEffort
}

// LogContextWindowChoice logs the selected context window for a role. The default source warns
// that the model is absent from the registry; users can set context_window explicitly to avoid
// premature compaction when the actual model window is larger.
func LogContextWindowChoice(role, model string, window int, source ContextWindowSource) {
	attrs := []any{"module", "context", "role", role, "model", model, "window", window, "source", source}
	switch source {
	case CtxWindowModelConfig:
		slog.Info("Context window (from provider model config)", attrs...)
	case CtxWindowDefault:
		slog.Warn("Unknown model; using fallback window (set providers.<name>.models[].context_window explicitly)", attrs...)
	case CtxWindowConfig:
		slog.Info("Context window (from config file context_window)", attrs...)
	default:
		slog.Info("Context window", attrs...)
	}
}

// CandidateModels returns models available for switching under a provider.
// Explicit provider models take priority; referenced models are added as well.
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
			add(model.Name)
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
	if err := c.validateProviderAPI(owner, ref.Provider, pc); err != nil {
		return err
	}
	return nil
}

func (c Config) validateProviderAPI(owner, providerName string, pc ProviderConfig) error {
	if pc.API == "" {
		return nil
	}
	providerType, err := pc.ProviderType(providerName)
	if err != nil {
		return fmt.Errorf("%s cannot resolve the protocol type for provider %q api configuration: %w", owner, providerName, err)
	}
	if strings.ToLower(strings.TrimSpace(providerType)) != "openai" {
		return fmt.Errorf("%s provider %q api only supports an OpenAI protocol provider: %w", owner, providerName, errs.ErrConfig)
	}
	return nil
}
