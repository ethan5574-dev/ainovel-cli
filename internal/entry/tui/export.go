package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/exp"
)

// exportDoneMsg is the final result of the /export command.
//
// Unlike /import which uses event streaming, export is synchronous local IO with no intermediate progress;
// it runs in a goroutine and posts this message once completed.
type exportDoneMsg struct {
	result *exp.Result
	err    error
}

// startExport parses arguments and returns a tea.Cmd.
// The actual export runs in tea.Cmd (to avoid blocking the UI) and posts exportDoneMsg when done.
func startExport(rt *host.Host, args []string) (tea.Cmd, error) {
	opts, err := parseExportArgs(args)
	if err != nil {
		return nil, err
	}
	return func() tea.Msg {
		// 30s is sufficient for local IO of a medium-long novel; timeout is a safety fallback to prevent hanging.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := rt.Export(ctx, opts)
		return exportDoneMsg{result: res, err: err}
	}, nil
}

// parseExportArgs parses `/export [path] [from=N] [to=M] [--overwrite]`.
//
// Positional parameters: at most one, as the output path; defaults to exp.Run ({novelDir}/{NovelName}.txt).
func parseExportArgs(args []string) (exp.Options, error) {
	var opts exp.Options
	for _, a := range args {
		if a == "--overwrite" {
			opts.Overwrite = true
			continue
		}
		if k, v, ok := strings.Cut(a, "="); ok {
			switch strings.ToLower(k) {
			case "from":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return exp.Options{}, fmt.Errorf("from must be a non-negative integer: %q", v)
				}
				opts.From = n
			case "to":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return exp.Options{}, fmt.Errorf("to must be a non-negative integer: %q", v)
				}
				opts.To = n
			default:
				return exp.Options{}, fmt.Errorf("unknown parameter %q (supported: from / to)", k)
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			return exp.Options{}, fmt.Errorf("unknown flag %q", a)
		}
		if opts.OutPath != "" {
			return exp.Options{}, fmt.Errorf("only one path parameter is supported: %q", a)
		}
		opts.OutPath = a
	}
	return opts, nil
}

// formatExportSuccess renders Result as an event Summary.
func formatExportSuccess(res *exp.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✓ Exported %d chapters / %s to %s", res.Chapters, humanBytes(res.Bytes), res.Path)
	if n := len(res.Skipped); n > 0 {
		fmt.Fprintf(&b, " (skipped %d unfinished chapters: %s)", n, briefIntList(res.Skipped, 5))
	}
	return b.String()
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func briefIntList(xs []int, max int) string {
	if len(xs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(xs))
	for i, x := range xs {
		if i >= max {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, strconv.Itoa(x))
	}
	return strings.Join(parts, ",")
}
