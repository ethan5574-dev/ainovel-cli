package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/exp"
)

// exportDoneMsg 是 /export 命令的最终结果。
//
// 不像 /import 走事件流：导出是同步本地 IO，没有中间进度可言；
// 在 goroutine 里跑完后一次性回投这条消息。
type exportDoneMsg struct {
	result *exp.Result
	err    error
}

// startExport 解析参数并返回 tea.Cmd。
// 真正的导出在 tea.Cmd 里跑（避免阻塞 UI），完成后投递 exportDoneMsg。
func startExport(rt *host.Host, args []string) (tea.Cmd, error) {
	opts, err := parseExportArgs(args)
	if err != nil {
		return nil, err
	}
	return func() tea.Msg {
		res, err := rt.Export(context.Background(), opts)
		return exportDoneMsg{result: res, err: err}
	}, nil
}

// parseExportArgs 解析 `/export [path] [from=N] [to=M] [--overwrite]`。
//
// 位置参数：最多一个，作为输出路径；缺省由 exp.Run 决定（{novelDir}/{NovelName}.txt）。
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
			return exp.Options{}, fmt.Errorf("only one path argument is supported: %q", a)
		}
		opts.OutPath = a
	}
	return opts, nil
}

// formatExportSuccess 把 Result 渲染成事件 Summary。
func formatExportSuccess(res *exp.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✓ Exported %d chapters / %s to %s", res.Chapters, humanBytes(res.Bytes), res.Path)
	if n := len(res.Skipped); n > 0 {
		fmt.Fprintf(&b, " (skipped %d incomplete chapters: %s)", n, briefIntList(res.Skipped, 5))
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
