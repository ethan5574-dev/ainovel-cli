package host

import "log/slog"

type newOptions struct {
	logFile       string
	logAlsoStderr bool
	logAttrs      []slog.Attr
}

// NewOption configures Host construction; Host retains runtime resource ownership.
type NewOption func(*newOptions)

// WithFileLog gives Host a runtime log session opened only after acquiring the novel-directory lease,
// and closed after all Host shutdown logs finish. If opening fails, retain the current process logger;
// callers must explicitly handle the failure through FileLogError.
func WithFileLog(filename string, alsoStderr bool, attrs ...slog.Attr) NewOption {
	return func(opts *newOptions) {
		opts.logFile = filename
		opts.logAlsoStderr = alsoStderr
		opts.logAttrs = append([]slog.Attr(nil), attrs...)
	}
}
