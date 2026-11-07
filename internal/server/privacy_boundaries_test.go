package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

// A browser page served from attacker.example whose DNS later rebinds to
// 127.0.0.1 is same-origin from the browser's view: no preflight, and it can
// read the response. The only server-side defence is a Host check.
func TestDNSRebindingNoAuth(t *testing.T) {
	var seen string
	fake := &nativeFake{complete: func(_ context.Context, prompt string) (runner.Completion, error) {
		seen = prompt
		return runner.Completion{Text: "model-output-readable-by-page", Model: runner.ModelLocal, Usage: &runner.Usage{InputTokens: 1, OutputTokens: 1}}, nil
	}}
	srv := httptest.NewServer(NewUnauthenticated(fake).Handler())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"local","messages":[{"role":"user","content":"REBIND-PROMPT"}]}`))
	req.Host = "attacker.example:18106"
	req.Header.Set("Origin", "http://attacker.example:18106")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	t.Logf("rebind POST: status=%d runner_calls=%d prompt_seen=%v body=%s", res.StatusCode, fake.calls.Load(), strings.Contains(seen, "REBIND-PROMPT"), body)

	// Cross-origin (no rebinding) preflight and simple request, for coverage.
	pre, _ := http.NewRequest(http.MethodOptions, srv.URL+"/v1/chat/completions", nil)
	pre.Header.Set("Origin", "http://evil.example")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	pre.Header.Set("Access-Control-Request-Headers", "content-type")
	pres, err := http.DefaultClient.Do(pre)
	if err != nil {
		t.Fatal(err)
	}
	defer pres.Body.Close()
	t.Logf("preflight: status=%d ACAO=%q", pres.StatusCode, pres.Header.Get("Access-Control-Allow-Origin"))
	simple, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(`{}`))
	simple.Header.Set("Origin", "http://evil.example")
	simple.Header.Set("Content-Type", "text/plain")
	sres, err := http.DefaultClient.Do(simple)
	if err != nil {
		t.Fatal(err)
	}
	defer sres.Body.Close()
	t.Logf("simple text/plain POST: status=%d calls_after=%d", sres.StatusCode, fake.calls.Load())
	if res.StatusCode != http.StatusForbidden || pres.StatusCode != http.StatusMethodNotAllowed || pres.Header.Get("Access-Control-Allow-Origin") != "" || sres.StatusCode != http.StatusForbidden || fake.calls.Load() != 0 || seen != "" {
		t.Errorf("foreign Host/Origin boundary failed: post=%d preflight=%d simple=%d calls=%d", res.StatusCode, pres.StatusCode, sres.StatusCode, fake.calls.Load())
	}
}

const privacyMarker = "QQ-PRIVACY-CANARY-QQ"

func TestMarkerFuzzHTTPErrors(t *testing.T) {
	cases := []struct{ path, body string }{
		{"/v1/chat/completions", `{"model":"MARKER","messages":[{"role":"user","content":"MARKER"}]}`},
		{"/v1/responses", `{"model":"MARKER","input":"MARKER"}`},
		{"/v1/chat/completions", `{"model":"local","MARKER":1,"messages":[{"role":"user","content":"MARKER"}]}`},
		{"/v1/responses", `{"model":"local","MARKER":1,"input":"MARKER"}`},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"MARKER"}],"temperature":"MARKER"}`},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"MARKER"}` + "MARKER"},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"MARKER"}}]}]}`},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/MARKER;base64,AAAA"}}]}]}`},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"file:///etc/MARKER"}}]}]}`},
		{"/v1/responses", `{"model":"local","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://MARKER.example/x.png"}]}]}`},
		{"/v1/responses", `{"model":"local","input":[{"role":"user","content":[{"type":"input_file","filename":"MARKER.pdf","file_data":"MARKER"}]}]}`},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"MARKER","name":"MARKER"}],"user":"MARKER","metadata":{"MARKER":"MARKER"}}`},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"MARKER"}],"stop":["MARKER"],"seed":"MARKER"}`},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"MARKER"}],"response_format":{"type":"json_schema","json_schema":{"name":"MARKER","schema":{"type":"MARKER"}}}}`},
		{"/v1/chat/completions", `{"model":"local","messages":[{"role":"user","content":"MARKER"}],"response_format":{"type":"json_schema","json_schema":{"name":"x","schema":{"type":"object","properties":{"MARKER":{"type":"string","pattern":"MARKER("}}}}}}`},
		{"/v1/chat/completions", `{"model":"local","tools":[{"type":"function","function":{"name":"MARKER","parameters":{"type":"object","properties":{"MARKER":{"type":"MARKER"}}}}}],"messages":[{"role":"user","content":"MARKER"}]}`},
		{"/v1/chat/completions", `{"model":"local","tools":[{"type":"MARKER","function":{"name":"x"}}],"messages":[{"role":"user","content":"MARKER"}]}`},
		{"/v1/images/generations", `{"model":"MARKER","prompt":"MARKER","size":"MARKER","style":"MARKER"}`},
		{"/v1/images/generations", `{"model":"hollis-image","prompt":"MARKER","size":"MARKER","response_format":"MARKER"}`},
		{"/v1/chat/completions", `{"model":"cloud","messages":[{"role":"user","content":"MARKER"}],"stream":true}`},
		{"/v1/chat/completions", `{"model":"cloud","messages":[{"role":"MARKER","content":"MARKER"}]}`},
	}
	for _, r := range []runner.Runner{&toolFake{}, &echoRunner{}} {
		for _, tc := range cases {
			body := strings.ReplaceAll(tc.body, "MARKER", privacyMarker)
			res := post(t, NewUnauthenticated(r).Handler(), tc.path, body)
			if res.Code >= 400 && strings.Contains(res.Body.String(), privacyMarker) {
				t.Errorf("%T %s %s -> %d leaks marker: %s", r, tc.path, tc.body, res.Code, res.Body.String())
			}
		}
	}
}
