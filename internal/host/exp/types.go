// Package exp implements the capability to export completed chapters.
//
// Symmetric with imp/: pure local IO, does not depend on LLM, does not modify store state. Export can run
// concurrently with the Coordinator (read-only Progress + final chapters), belonging to sidecar capabilities.
package exp

import "github.com/voocel/ainovel-cli/internal/store"

// Format identifies the export format.
type Format string

const (
	// FormatTXT plain text output.
	FormatTXT Format = "txt"
	// FormatEPUB standard EPUB 3 container (zip + xhtml).
	FormatEPUB Format = "epub"
)

// Options controls the export behavior. Zero-value is equivalent to "export the entire book to default path, return error if file exists".
//
// Layout: "《Book Title》" -> Volume dividers -> Chapter text. Two types of internal data are not exported: premise (creation blueprint,
// containing background metadata such as target readers / core selling points / writing guidelines for the author and engine, not the reader's preface);
// arc dividers (arcs are too detailed for the reader's perspective). Book title and volume dividers are always retained.
type Options struct {
	// Format: if empty, inferred from OutPath extension (.txt -> TXT, .epub -> EPUB);
	// defaults to FormatTXT if OutPath is also empty. SDK callers can specify it explicitly to skip inference.
	Format Format

	// OutPath: output file path; empty means {novelDir}/{NovelName}.{ext},
	// where ext is determined by Format (if NovelName is empty, the directory name is used).
	OutPath string

	// From / To: chapter range, closed interval. 0 means from chapter 1 / to the last chapter.
	// Uncompleted chapters in range will be skipped and written to Result.Skipped, not treated as an error.
	From, To int

	// Overwrite: whether to overwrite if file exists; defaults to false.
	Overwrite bool
}

// Deps: dependencies for Run. Only store; export doesn't require LLM, prompt, or bundle.
type Deps struct {
	Store *store.Store
}

// Result: summary of a successful export.
type Result struct {
	// Path: actual path of the written file (absolute or relative from the caller).
	Path string
	// Chapters: number of chapters actually written.
	Chapters int
	// Bytes: file size in bytes (UTF-8).
	Bytes int
	// Skipped: chapter numbers in the requested range that are not completed.
	Skipped []int
}
