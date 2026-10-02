package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/entry/headless"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/entry/tui"
	"github.com/voocel/ainovel-cli/internal/eval"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/rules"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
	"github.com/voocel/ainovel-cli/internal/web"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// headlessMode records whether this run uses headless mode, so die can decide whether to pause on exit.
var headlessMode bool

func main() {
	// Intercept the subcommand before regular flag parsing: eval is an offline evaluation harness with its own flags.
	if len(os.Args) > 1 && os.Args[1] == "eval" {
		os.Exit(eval.Command(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "web" {
		runWeb(os.Args[2:])
		return
	}

	opts, args, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		die("flags: %v", err)
	}
	if opts.Version {
		buildversion.Print(os.Stdout, versionInfo())
		return
	}
	if opts.Update {
		if err := runSelfUpdate(opts.UpdateVersion); err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
		}
		return
	}
	headlessMode = opts.Headless

	// First-run setup.
	if bootstrap.NeedsSetup() {
		if opts.Headless {
			die("error: headless mode does not support first-run setup; run the TUI once to finish configuration")
		}
		setupCfg, err := bootstrap.RunSetup()
		if err != nil {
			die("setup: %v", err)
		}
		// Continue with the generated configuration after setup.
		runWithConfig(setupCfg, opts, args)
		return
	}

	// Load configuration.
	cfg, err := bootstrap.LoadConfig()
	if err != nil {
		die("config: %v", err)
	}

	runWithConfig(cfg, opts, args)
}

// die handles fatal exits uniformly: print to stderr, write to ~/.ainovel/last-error.log,
// and pause for Enter in an interactive terminal so the message does not disappear immediately.
func die(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, msg)
	if path := bootstrap.WriteStartupError(msg); path != "" {
		fmt.Fprintf(os.Stderr, "(Detailed error written to %s)\n", path)
	}
	if !headlessMode && stdinIsTerminal() {
		fmt.Fprint(os.Stderr, "\nPress Enter to exit...")
		fmt.Fscanln(os.Stdin)
	}
	os.Exit(1)
}

// stdinIsTerminal reports whether stdin is connected to a terminal (character device).
// It is true for interactive terminals and false for pipes, redirects, and CI.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func runWithConfig(cfg bootstrap.Config, opts cliOptions, args []string) {
	rules.EnsureHomeRulesDir()

	if len(args) > 0 {
		die("error: passing the novel request on the command line is no longer supported; enter it in the TUI after launch")
	}

	// FillDefaults must run before asset loading: OutputDir is a runtime field whose default
	// is normalized here; otherwise the book-level style override is never loaded.
	cfg.FillDefaults()
	bundle := assets.Load(cfg.Style, assets.DefaultLoadOptions(cfg.OutputDir))
	if opts.Headless {
		prompt, err := loadPrompt(opts)
		if err != nil {
			die("error: %v", err)
		}
		if err := headless.Run(cfg, bundle, headless.Options{Prompt: prompt}); err != nil {
			die("error: %v", err)
		}
		return
	}
	if opts.Prompt != "" || opts.PromptFile != "" {
		die("error: --prompt/--prompt-file can only be used with --headless")
	}
	if err := tui.Run(cfg, bundle, versionInfo()); err != nil {
		die("error: %v", err)
	}
}

func runWeb(argv []string) {
	addr, err := parseWebAddress(argv)
	if err != nil {
		die("web flags: %v", err)
	}
	// Unlike TUI/headless, web mode doesn't require configuration to already exist: with no
	// runtime attached, the server still serves the onboarding wizard (GET /api/setup/status
	// reports needsSetup=true) so a fresh machine can be configured entirely from the browser.
	var runtime *host.Host
	if !bootstrap.NeedsSetup() {
		cfg, err := bootstrap.LoadConfig()
		if err != nil {
			die("config: %v", err)
		}
		cfg.FillDefaults()
		bundle := assets.Load(cfg.Style, assets.DefaultLoadOptions(cfg.OutputDir))
		runtime, err = host.New(cfg, bundle)
		if err != nil {
			die("web runtime: %v", err)
		}
	}
	if err := web.Run(runtime, addr); err != nil {
		die("web server: %v", err)
	}
}

func parseWebAddress(argv []string) (string, error) {
	addr := "127.0.0.1:5173"
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--addr":
			if i+1 >= len(argv) {
				return "", fmt.Errorf("--addr is missing a value")
			}
			addr = argv[i+1]
			i++
		case "--port":
			if i+1 >= len(argv) {
				return "", fmt.Errorf("--port is missing a value")
			}
			addr = "127.0.0.1:" + argv[i+1]
			i++
		default:
			return "", fmt.Errorf("unknown web option %q", argv[i])
		}
	}
	return addr, nil
}

type cliOptions struct {
	Headless      bool
	Prompt        string
	PromptFile    string
	Version       bool
	Update        bool
	UpdateVersion string
}

// parseCLIOptions parses CLI flags and returns the options plus remaining arguments.
func parseCLIOptions(argv []string) (cliOptions, []string, error) {
	var opts cliOptions
	var args []string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--version", "-v":
			opts.Version = true
		case "version":
			if i+1 < len(argv) {
				return opts, nil, fmt.Errorf("version does not accept arguments")
			}
			opts.Version = true
		case "update":
			if opts.Update {
				return opts, nil, fmt.Errorf("update can only be specified once")
			}
			opts.Update = true
			if i+1 < len(argv) {
				if strings.HasPrefix(argv[i+1], "-") {
					return opts, nil, fmt.Errorf("update accepts at most one optional version argument")
				}
				opts.UpdateVersion = argv[i+1]
				i++
			}
			if i+1 < len(argv) {
				return opts, nil, fmt.Errorf("update accepts at most one optional version argument")
			}
		case "--headless":
			opts.Headless = true
		case "--prompt":
			if i+1 >= len(argv) {
				return opts, nil, fmt.Errorf("--prompt is missing a value")
			}
			opts.Prompt = argv[i+1]
			i++
		case "--prompt-file":
			if i+1 >= len(argv) {
				return opts, nil, fmt.Errorf("--prompt-file is missing a value")
			}
			opts.PromptFile = argv[i+1]
			i++
		default:
			args = append(args, argv[i])
		}
	}
	if opts.Prompt != "" && opts.PromptFile != "" {
		return opts, nil, fmt.Errorf("--prompt and --prompt-file cannot be used together")
	}
	if opts.Version && (opts.Update || opts.Headless || opts.Prompt != "" || opts.PromptFile != "" || len(args) > 0) {
		return opts, nil, fmt.Errorf("version cannot be combined with other startup flags")
	}
	if opts.Update && (opts.Headless || opts.Prompt != "" || opts.PromptFile != "" || len(args) > 0) {
		return opts, nil, fmt.Errorf("update cannot be combined with other startup flags")
	}
	return opts, args, nil
}

func versionInfo() buildversion.Info {
	return buildversion.Resolve(buildversion.Info{
		Version: version,
		Commit:  commit,
		Date:    date,
	})
}

func runSelfUpdate(target string) error {
	info := versionInfo()
	result, err := buildversion.Update(context.Background(), buildversion.UpdateOptions{
		Repo:           buildversion.DefaultRepo,
		BinaryName:     "ainovel-cli",
		TargetVersion:  target,
		CurrentVersion: info.Version,
	})
	if err != nil {
		return err
	}
	if !result.Updated {
		fmt.Printf("ainovel-cli is already at version %s\n", result.Version)
		return nil
	}
	fmt.Printf("ainovel-cli updated to %s\n", result.Version)
	fmt.Printf("Installed at: %s\n", result.Path)
	return nil
}

func loadPrompt(opts cliOptions) (string, error) {
	return loadPromptFrom(opts, os.Stdin)
}

func loadPromptFrom(opts cliOptions, stdin io.Reader) (string, error) {
	if opts.PromptFile == "" {
		return strings.TrimSpace(opts.Prompt), nil
	}

	if opts.PromptFile == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("读取 prompt 失败: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return startup.LoadPromptFile(opts.PromptFile)
}
