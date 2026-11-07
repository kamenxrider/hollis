package server

import (
	"strings"
	"testing"
)

func TestMissingNamedFunctionDiagnostic(t *testing.T) {
	for _, endpoint := range []struct{ path, input, tools, choice string }{
		{"/v1/chat/completions", `"messages":[{"role":"user","content":"weather"}]`,
			httpTools, `{"type":"function","function":{"name":"missing"}}`},
		{"/v1/responses", `"input":"weather"`,
			responsesHTTPTools, `{"type":"function","name":"missing"}`},
	} {
		fake := &toolFake{}
		res := post(t, NewUnauthenticated(fake).Handler(), endpoint.path,
			`{"model":"local","tools":`+endpoint.tools+`,"tool_choice":`+endpoint.choice+`,`+endpoint.input+`}`)
		if res.Code != 400 || fake.calls.Load() != 0 ||
			!strings.Contains(res.Body.String(), `"code":"unsupported_parameter"`) ||
			!strings.Contains(res.Body.String(), "tool_choice function was not offered") ||
			strings.Contains(res.Body.String(), "missing") ||
			strings.Contains(res.Body.String(), "get_weather") {
			t.Fatalf("status=%d calls=%d body=%s", res.Code, fake.calls.Load(), res.Body.String())
		}
	}
}
