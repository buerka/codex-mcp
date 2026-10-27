// Package output redacts secrets and pages large tool output.
package output

import "regexp"

var redactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), "[REDACTED PRIVATE KEY]"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`), "[REDACTED API KEY]"},
	{regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`), "Bearer [REDACTED]"},
	{regexp.MustCompile(`(?i)\bOPENAI_API_KEY\s*=\s*\S+`), "OPENAI_API_KEY=[REDACTED]"},
}

// Redact scrubs obvious credential material before text reaches a model.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllLiteralString(s, r.repl)
	}
	return s
}
