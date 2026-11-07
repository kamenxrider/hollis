package server

import "github.com/kamenxrider/hollis/internal/runner"

// The SDK exposes usage, not a finish reason. Its token ceiling is approximate,
// so this is deliberately a best-effort signal, not proof of truncation.
func finishReason(usage *runner.Usage, options *runner.GenerationOptions) string {
	if usage != nil && options != nil && options.MaxTokens != nil && usage.OutputTokens >= *options.MaxTokens {
		return "length"
	}
	return "stop"
}

func responseTermination(response map[string]any, reason string) {
	if reason != "length" {
		return
	}
	response["status"] = "incomplete"
	response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	if output, ok := response["output"].([]map[string]any); ok {
		for _, item := range output {
			item["status"] = "incomplete"
		}
	}
}
