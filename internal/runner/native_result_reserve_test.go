package runner

import "testing"

func TestNativeResultAdmissionAnswerReserve(t *testing.T) {
	for _, maximum := range []int{0, 128, 1024} {
		conversation := nativeConversation(t, ToolChoiceAuto)
		conversation.ResultSafetyTokens = 128
		if maximum != 0 {
			conversation.Options = &GenerationOptions{MaxTokens: &maximum}
		}
		r := &nativeScript{replies: []Completion{nativeReply("one", "get_weather", `{"city":"Tokyo"}`), {Text: "Sunny."}}}
		out, err := RunToolLoop(t.Context(), r, ModelLocal, conversation, &recordingExecutor{output: "sunny"})
		if err != nil || out.Text != "Sunny." || len(r.checks) != 1 {
			t.Fatal("admission failed", err)
		}
		reserved := max(512, maximum) + 128
		if got := r.checks[0].Options; got == nil || got.MaxTokens == nil || *got.MaxTokens != reserved {
			t.Fatal("wrong SDK answer reservation")
		}
		if r.requests[1].Options != conversation.Options || (maximum != 0 && *conversation.Options.MaxTokens != maximum) {
			t.Fatal("admission modified the requested generation options")
		}
	}
}
