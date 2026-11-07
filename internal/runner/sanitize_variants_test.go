package runner

import (
	"strings"
	"testing"
)

func TestSanitizeAppleTextVariants(t *testing.T) {
	const m = "QQ-PRIVACY-CANARY-QQ"
	prompt := "USER:\nplease summarise \"" + m + "\" for me today\n"
	for name, echoed := range map[string]string{
		"exact line":     `Error: please summarise "` + m + `" for me today`,
		"escaped quotes": `Error Domain=WFAskLLMActionErrorDomain Code=5 UserInfo={NSLocalizedDescription=Input "please summarise \"` + m + `\" for me today"}`,
		"truncated":      `Error: Could not process input "please summarise "` + m + `" for m…"`,
		"case changed":   `error: PLEASE SUMMARISE "` + strings.ToUpper(m) + `" FOR ME TODAY`,
	} {
		out := sanitizeAppleText(echoed, prompt)
		t.Logf("%-15s leaks=%v -> %s", name, strings.Contains(strings.ToUpper(out), m), out)
	}
}
