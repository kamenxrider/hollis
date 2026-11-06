package runner

// Only this closed numeric vocabulary may enter opt-in budget diagnostics.
func validNativeBudget(counts map[string]int) bool {
	keys := []string{
		"base_tokens", "instructions_tokens", "tools_tokens", "conversation_tokens",
		"results_framing_tokens", "input_tokens", "answer_reserved_tokens", "context_tokens",
		"tools_bytes", "instructions_bytes", "conversation_bytes", "results_framing_bytes",
	}
	if len(counts) != len(keys) {
		return false
	}
	for _, key := range keys {
		value, ok := counts[key]
		if !ok || value < 0 || value > 1<<30 {
			return false
		}
	}
	return counts["base_tokens"]+counts["instructions_tokens"]+counts["tools_tokens"]+
		counts["conversation_tokens"]+counts["results_framing_tokens"] == counts["input_tokens"]
}
