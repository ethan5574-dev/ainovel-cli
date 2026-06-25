package utils

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// DecodeText decodes the user-provided text file bytes to UTF-8. If the text is invalid UTF-8,
// it falls back to GB18030 (a GBK superset) transcoding. Network-sourced Chinese novel TXT files
// are often GBK encoded and reading them directly as UTF-8 results in garbage text. Non-GBK byte
// sequences will be replaced with U+FFFD by the decoder. Finally, it strips the UTF-8 BOM if present.
func DecodeText(data []byte) string {
	if !utf8.Valid(data) {
		if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
			data = decoded
		}
	}
	return strings.TrimPrefix(string(data), "\uFEFF")
}
