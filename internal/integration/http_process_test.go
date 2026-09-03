// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package integration_test

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCLIProcessHTTPServerContracts(t *testing.T) {
	state := t.TempDir()
	token := strings.Repeat("h", 32)
	tokenFile := filepath.Join(state, "token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCLIHelperProcess$", "--", "serve", "--addr", "127.0.0.1:0", "--token-file", tokenFile)
	cmd.Env = isolatedHollisEnv(state, "GO_WANT_HOLLIS_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stop := func() {
		if cmd.Process == nil {
			return
		}
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		cmd.Process = nil
	}
	t.Cleanup(stop)

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		stop()
		if strings.Contains(stderr.String(), "operation not permitted") {
			t.Skip("sandbox forbids loopback listeners; CI and the host-route gate exercise this test")
		}
		t.Fatalf("serve did not announce a URL; stderr=%q", stderr.String())
	}
	line := scanner.Text()
	urlStart := strings.Index(line, "http://")
	if urlStart < 0 {
		stop()
		t.Fatalf("serve announcement has no URL: %q", line)
	}
	baseURL := line[urlStart:]
	if cut := strings.IndexByte(baseURL, ' '); cut >= 0 {
		baseURL = baseURL[:cut]
	}
	client := &http.Client{Timeout: 5 * time.Second}

	status, body := processHTTP(t, client, http.MethodGet, baseURL+"/health", "", "")
	if status != http.StatusOK || !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("health: status=%d body=%q", status, body)
	}

	status, body = processHTTP(t, client, http.MethodGet, baseURL+"/v1/models", "", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("models without auth: status=%d body=%q", status, body)
	}
	status, body = processHTTP(t, client, http.MethodGet, baseURL+"/v1/models", "", token)
	if status != http.StatusOK {
		t.Fatalf("models with auth: status=%d body=%q", status, body)
	}
	var models map[string]any
	decodeObject(t, body, &models)
	if models["object"] != "list" {
		t.Fatalf("models object=%#v", models["object"])
	}

	chatBody := `{"model":"cloud","messages":[{"role":"user","content":"process HTTP marker"}]}`
	status, body = processHTTP(t, client, http.MethodPost, baseURL+"/v1/chat/completions", chatBody, token)
	if status != http.StatusOK {
		t.Fatalf("chat completion: status=%d body=%q", status, body)
	}
	var completion map[string]any
	decodeObject(t, body, &completion)
	if completion["object"] != "chat.completion" || completion["model"] != "cloud" {
		t.Fatalf("completion envelope=%#v", completion)
	}

	status, body = processHTTP(t, client, http.MethodPost, baseURL+"/v1/chat/completions", `{"model":"cloud","stream":true,"messages":[{"role":"user","content":"marker"}]}`, token)
	if status != http.StatusBadRequest || !strings.Contains(body, "streaming is not supported") {
		t.Fatalf("chat streaming: status=%d body=%q", status, body)
	}
	status, body = processHTTP(t, client, http.MethodPost, baseURL+"/v1/chat/completions", `{"model":"unknown","messages":[{"role":"user","content":"marker"}]}`, token)
	var unknownModel map[string]any
	decodeObject(t, body, &unknownModel)
	if status != http.StatusBadRequest || unknownModel["error"] == nil {
		t.Fatalf("chat unknown model: status=%d body=%q", status, body)
	}
	status, body = processHTTP(t, client, http.MethodPost, baseURL+"/v1/chat/completions", "{", token)
	var invalidJSON map[string]any
	decodeObject(t, body, &invalidJSON)
	if status != http.StatusBadRequest || invalidJSON["error"] == nil {
		t.Fatalf("chat invalid JSON: status=%d body=%q", status, body)
	}

	status, body = processHTTP(t, client, http.MethodPost, baseURL+"/v1/responses", `{"model":"cloud","input":"response marker"}`, token)
	if status != http.StatusOK {
		t.Fatalf("responses: status=%d body=%q", status, body)
	}
	var response map[string]any
	decodeObject(t, body, &response)
	if response["object"] != "response" || response["status"] != "completed" {
		t.Fatalf("response envelope=%#v", response)
	}
	status, body = processHTTP(t, client, http.MethodPost, baseURL+"/v1/responses", `{"model":"cloud","input":"marker","stream":true}`, token)
	if status != http.StatusBadRequest || !strings.Contains(body, "streaming is not supported") {
		t.Fatalf("responses streaming: status=%d body=%q", status, body)
	}

	status, body = processHTTP(t, client, http.MethodGet, baseURL+"/v1/chat/completions", "", token)
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("chat wrong method: status=%d body=%q", status, body)
	}
	status, body = processHTTP(t, client, http.MethodGet, baseURL+"/not-found", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("unknown route: status=%d body=%q", status, body)
	}
}

func processHTTP(t *testing.T, client *http.Client, method, url, body, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP %s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(raw)
}
