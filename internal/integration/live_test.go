// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
//go:build hollis_live

package integration_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const proSpacing = 45 * time.Second

type liveHarness struct {
	t             *testing.T
	bin           string
	state         string
	marker        string
	lastPro       time.Time
	mu            sync.Mutex
	report        liveReport
	serverStopped bool
	chatDeleted   bool
	stateRemoved  bool
}

type liveReport struct {
	Revision            string            `json:"revision"`
	TrackedDiffSHA256   string            `json:"tracked_diff_sha256"`
	WorktreeSHA256      string            `json:"worktree_sha256"`
	OSVersion           string            `json:"os_version"`
	OSBuild             string            `json:"os_build"`
	BridgeSHA256        map[string]string `json:"bridge_sha256"`
	StartedAt           string            `json:"started_at"`
	FinishedAt          string            `json:"finished_at"`
	Outcome             string            `json:"outcome"`
	Records             []liveRecord      `json:"records"`
	CloudProPlanned     int               `json:"cloud_pro_planned"`
	CloudProStarted     int               `json:"cloud_pro_started"`
	ConversationDeleted bool              `json:"conversation_deleted"`
	ServerStopped       bool              `json:"server_stopped"`
	TemporaryStateGone  bool              `json:"temporary_state_removed"`
}

type liveRecord struct {
	Operation      string `json:"operation"`
	Status         string `json:"status"`
	DurationMS     int64  `json:"duration_ms"`
	ModelRequested string `json:"model_requested,omitempty"`
	ModelUsed      string `json:"model_used,omitempty"`
}

func TestLiveRealSystem(t *testing.T) {
	if os.Getenv("HOLLIS_LIVE") != "1" {
		t.Skip("set HOLLIS_LIVE=1 to authorize real Shortcuts model calls")
	}
	bin := os.Getenv("HOLLIS_BIN")
	if !filepath.IsAbs(bin) {
		t.Fatal("HOLLIS_BIN must name the absolute path of the exact binary under test")
	}
	h := &liveHarness{
		t: t, bin: bin, state: t.TempDir(), marker: fmt.Sprintf("hollis-live-%d", time.Now().UnixNano()),
		report: collectLiveReportIdentity(t),
	}
	h.report.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	h.report.CloudProPlanned = 6
	defer h.writeReport()

	for _, args := range [][]string{
		nil,
		{"--help"},
		{"help", "respond"},
		{"--version"},
		{"version"},
		{"completion", "zsh"},
		{"agent-context"},
		{"config", "show", "--json"},
		{"config", "set", "model", "auto", "--json"},
		{"models", "--json"},
		{"doctor", "--json"},
		{"chats", "list", "--json"},
	} {
		h.mustCLI(args...)
	}
	h.mustCLI("config", "set", "bridge", "cloud", "temporary-live-override", "--json")
	h.mustCLI("config", "set", "bridge", "cloud", "", "--json")

	for _, model := range []string{"auto", "cloud", "on-device", "chatgpt"} {
		out := h.mustCLI("respond", "--json", "--timeout", "120s", "--model", model, h.quietPrompt("respond-"+model))
		assertModelResponse(t, out, model)
	}

	// Six useful Cloud Pro calls, never concurrent and never closer than the
	// agreed quiet interval. Any failure stops the test; there is no retry.
	proRespond := h.mustProCLI("respond", "--json", "--timeout", "120s", "--model", "cloud-pro", h.quietPrompt("pro-respond"))
	assertModelResponse(t, proRespond, "cloud-pro")

	created := h.mustProCLI("chat", "--json", "--timeout", "120s", "--model", "cloud-pro", "Remember the neutral word amber. "+h.marker+"-new-chat")
	var chat map[string]any
	decodeObject(t, created, &chat)
	id, _ := chat["conversation_id"].(string)
	if id == "" {
		t.Fatal("Cloud Pro chat did not return a conversation_id")
	}
	t.Cleanup(func() {
		if !h.chatDeleted {
			_ = h.cli("chats", "delete", "--yes", id)
		}
	})

	continued := h.mustProCLI("chat", "--json", "--timeout", "120s", "--continue", id, "Name the word from the prior turn. "+h.marker+"-continue")
	assertModelResponse(t, continued, "cloud-pro")

	agent := h.mustProCLI("--agent", "respond", "--timeout", "120s", "--model", "cloud-pro", h.quietPrompt("pro-agent"))
	var wrapped map[string]any
	decodeObject(t, agent, &wrapped)
	results, ok := wrapped["results"].(map[string]any)
	if !ok {
		t.Fatal("Cloud Pro agent response lacks results")
	}
	if results["model_requested"] != "cloud-pro" || results["model_used"] != "cloud-pro" || strings.TrimSpace(fmt.Sprint(results["response"])) == "" {
		t.Fatal("Cloud Pro agent response has invalid model metadata or empty output")
	}

	h.mustCLI("chats", "list", "--json")
	h.mustCLI("chats", "show", id, "--json")
	h.mustCLI("chats", "search", h.marker+"-continue", "--json")
	h.mustCLI("chats", "rename", id, h.marker+"-renamed", "--json")

	baseURL, token, stop := h.startServer()
	t.Cleanup(stop)
	h.mustHTTP(http.MethodGet, baseURL+"/health", "", "", http.StatusOK)
	h.mustHTTP(http.MethodGet, baseURL+"/v1/models", "", "", http.StatusUnauthorized)
	h.mustHTTP(http.MethodGet, baseURL+"/v1/models", "", token, http.StatusOK)
	h.mustHTTP(http.MethodPost, baseURL+"/health", `{}`, token, http.StatusMethodNotAllowed)
	h.mustHTTP(http.MethodPost, baseURL+"/v1/models", `{}`, token, http.StatusMethodNotAllowed)
	h.mustHTTP(http.MethodGet, baseURL+"/v1/responses", "", token, http.StatusMethodNotAllowed)
	h.mustHTTP(http.MethodPost, baseURL+"/v1/responses", `{`, token, http.StatusBadRequest)
	h.mustHTTP(http.MethodPost, baseURL+"/v1/responses", `{"input":"quiet","unknown":true}`, token, http.StatusBadRequest)
	h.mustHTTP(http.MethodPost, baseURL+"/v1/responses", `{"model":"cloud-pro","input":""}`, token, http.StatusBadRequest)
	h.mustHTTP(http.MethodPost, baseURL+"/v1/chat/completions", `{"model":"cloud-pro","stream":true,"messages":[{"role":"user","content":"quiet"}]}`, token, http.StatusBadRequest)

	h.pacePro()
	h.report.CloudProStarted++
	completion := h.mustHTTP(http.MethodPost, baseURL+"/v1/chat/completions", fmt.Sprintf(`{"model":"cloud-pro","stream":false,"messages":[{"role":"user","content":%q}]}`, h.quietPrompt("pro-chat-completions")), token, http.StatusOK)
	assertHTTPModelResponse(t, completion, "cloud-pro", "choices")
	h.pacePro()
	h.report.CloudProStarted++
	response := h.mustHTTP(http.MethodPost, baseURL+"/v1/responses", fmt.Sprintf(`{"model":"cloud-pro","stream":false,"input":%q}`, h.quietPrompt("pro-responses")), token, http.StatusOK)
	assertHTTPModelResponse(t, response, "cloud-pro", "output")

	h.mustCLI("chats", "delete", "--yes", id, "--json")
	h.chatDeleted = true
	deleted := h.cli("chats", "show", id, "--json")
	if deleted.exit != 3 {
		t.Fatalf("deleted conversation remained visible; exit=%d", deleted.exit)
	}

	stop()
	if !h.serverStopped {
		t.Fatal("server did not stop cleanly")
	}
	if err := os.RemoveAll(h.state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.state); !os.IsNotExist(err) {
		t.Fatalf("temporary state still exists; stat error=%v", err)
	}
	h.stateRemoved = true
}

func (h *liveHarness) quietPrompt(label string) string {
	return "Reply only with OK. Test marker " + h.marker + "-" + label + "."
}

func (h *liveHarness) cli(args ...string) processResult {
	return runProcess(h.bin, args, h.state, nil)
}

func (h *liveHarness) mustCLI(args ...string) string {
	h.t.Helper()
	started := time.Now()
	result := h.cli(args...)
	h.record(liveRecordFromCLI(args, result, time.Since(started)))
	if result.exit != 0 {
		h.t.Fatalf("command %s failed with exit %d; stderr omitted", safeCLIName(args), result.exit)
	}
	return result.stdout
}

func (h *liveHarness) mustProCLI(args ...string) string {
	h.t.Helper()
	h.pacePro()
	h.mu.Lock()
	h.report.CloudProStarted++
	h.mu.Unlock()
	return h.mustCLI(args...)
}

func (h *liveHarness) pacePro() {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if wait := proSpacing - time.Since(h.lastPro); !h.lastPro.IsZero() && wait > 0 {
		time.Sleep(wait)
	}
	h.lastPro = time.Now()
}

func (h *liveHarness) startServer() (baseURL, token string, stop func()) {
	h.t.Helper()
	token = strings.Repeat("h", 32)
	tokenFile := filepath.Join(h.state, "live-token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
	cmd := exec.Command(h.bin, "serve", "--addr", "127.0.0.1:0", "--token-file", tokenFile)
	cmd.Env = isolatedHollisEnv(h.state)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case waitErr := <-done:
			h.serverStopped = waitErr == nil
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}

	lineCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(io.MultiReader(stdout))
		if scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}()
	select {
	case line := <-lineCh:
		const marker = "listening on http://"
		idx := strings.Index(line, marker)
		if idx < 0 {
			stop()
			h.t.Fatalf("server did not announce a listening URL; stderr omitted")
		}
		baseURL = strings.TrimSpace(line[idx+len("listening on "):])
		if cut := strings.IndexByte(baseURL, ' '); cut >= 0 {
			baseURL = baseURL[:cut]
		}
	case <-time.After(10 * time.Second):
		stop()
		h.t.Fatalf("server did not start within 10s; stderr omitted")
	}
	return baseURL, token, stop
}

func (h *liveHarness) mustHTTP(method, url, body, token string, want int) map[string]any {
	h.t.Helper()
	started := time.Now()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 130 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		h.record(liveRecord{Operation: safeHTTPName(method, url), Status: "transport_error", DurationMS: time.Since(started).Milliseconds(), ModelRequested: requestedModel(body)})
		h.t.Fatalf("%s failed: %v", safeHTTPName(method, url), err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	if response.StatusCode != want {
		h.record(liveRecord{Operation: safeHTTPName(method, url), Status: fmt.Sprintf("http_%d", response.StatusCode), DurationMS: time.Since(started).Milliseconds(), ModelRequested: requestedModel(body)})
		h.t.Fatalf("%s status=%d want=%d; body omitted", safeHTTPName(method, url), response.StatusCode, want)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		h.t.Fatalf("%s returned non-JSON", safeHTTPName(method, url))
	}
	used, _ := payload["model"].(string)
	h.record(liveRecord{Operation: safeHTTPName(method, url), Status: fmt.Sprintf("http_%d", response.StatusCode), DurationMS: time.Since(started).Milliseconds(), ModelRequested: requestedModel(body), ModelUsed: used})
	return payload
}

func (h *liveHarness) record(record liveRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.report.Records = append(h.report.Records, record)
}

func liveRecordFromCLI(args []string, result processResult, elapsed time.Duration) liveRecord {
	record := liveRecord{Operation: safeCLIName(args), Status: fmt.Sprintf("exit_%d", result.exit), DurationMS: elapsed.Milliseconds(), ModelRequested: requestedCLIModel(args)}
	if result.exit != 0 || !json.Valid([]byte(result.stdout)) {
		return record
	}
	var payload map[string]any
	if json.Unmarshal([]byte(result.stdout), &payload) != nil {
		return record
	}
	if results, ok := payload["results"].(map[string]any); ok {
		payload = results
	}
	if requested, ok := payload["model_requested"].(string); ok {
		record.ModelRequested = requested
	}
	record.ModelUsed, _ = payload["model_used"].(string)
	return record
}

func requestedCLIModel(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--model" {
			return args[i+1]
		}
	}
	return ""
}

func safeCLIName(args []string) string {
	commands := map[string]bool{"respond": true, "chat": true, "chats": true, "models": true, "config": true, "serve": true, "doctor": true, "agent-context": true, "version": true, "help": true, "completion": true}
	for i, arg := range args {
		if !commands[arg] {
			continue
		}
		name := "hollis " + arg
		if (arg == "chats" || arg == "config" || arg == "completion" || arg == "help") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			name += " " + args[i+1]
		}
		return name
	}
	if len(args) == 0 {
		return "hollis"
	}
	if args[0] == "--version" {
		return "hollis --version"
	}
	return "hollis flags"
}

func safeHTTPName(method, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return method + " endpoint"
	}
	return method + " " + parsed.Path
}

func requestedModel(body string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil {
		return ""
	}
	model, _ := payload["model"].(string)
	return model
}

func assertHTTPModelResponse(t *testing.T, payload map[string]any, model, contentField string) {
	t.Helper()
	if payload["model"] != model {
		t.Fatalf("HTTP model_used=%v want=%s", payload["model"], model)
	}
	items, ok := payload[contentField].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("HTTP response has no %s items", contentField)
	}
	var text string
	first, _ := items[0].(map[string]any)
	switch contentField {
	case "choices":
		message, _ := first["message"].(map[string]any)
		text, _ = message["content"].(string)
	case "output":
		content, _ := first["content"].([]any)
		if len(content) > 0 {
			part, _ := content[0].(map[string]any)
			text, _ = part["text"].(string)
		}
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("HTTP model response was empty")
	}
}

func collectLiveReportIdentity(t *testing.T) liveReport {
	t.Helper()
	repo := repoRoot(t)
	output := func(name string, args ...string) []byte {
		cmd := exec.Command(name, args...)
		cmd.Dir = repo
		raw, err := cmd.Output()
		if err != nil {
			t.Fatalf("collect live-test identity with %s: %v", name, err)
		}
		return raw
	}
	revision := strings.TrimSpace(string(output("git", "rev-parse", "HEAD")))
	diff := output("git", "diff", "--binary", "HEAD")
	status := output("git", "status", "--porcelain=v1", "-z")
	untracked := output("git", "ls-files", "--others", "--exclude-standard", "-z")
	worktreeHash := sha256.New()
	worktreeHash.Write(diff)
	worktreeHash.Write(status)
	for _, rawPath := range bytes.Split(untracked, []byte{0}) {
		if len(rawPath) == 0 {
			continue
		}
		worktreeHash.Write(rawPath)
		if raw, err := os.ReadFile(filepath.Join(repo, string(rawPath))); err == nil {
			fileHash := sha256.Sum256(raw)
			worktreeHash.Write(fileHash[:])
		}
	}
	bridgeHashes := map[string]string{}
	paths, err := filepath.Glob(filepath.Join(repo, "bridges", "*.shortcut"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if strings.HasSuffix(path, ".signed.shortcut") {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(raw)
		bridgeHashes[filepath.Base(path)] = fmt.Sprintf("%x", hash)
	}
	return liveReport{
		Revision: revision, TrackedDiffSHA256: fmt.Sprintf("%x", sha256.Sum256(diff)), WorktreeSHA256: fmt.Sprintf("%x", worktreeHash.Sum(nil)),
		OSVersion:    strings.TrimSpace(string(output("/usr/bin/sw_vers", "-productVersion"))),
		OSBuild:      strings.TrimSpace(string(output("/usr/bin/sw_vers", "-buildVersion"))),
		BridgeSHA256: bridgeHashes,
	}
}

func (h *liveHarness) writeReport() {
	path := strings.TrimSpace(os.Getenv("HOLLIS_LIVE_REPORT"))
	if path == "" {
		return
	}
	h.mu.Lock()
	h.report.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	h.report.ConversationDeleted = h.chatDeleted
	h.report.ServerStopped = h.serverStopped
	h.report.TemporaryStateGone = h.stateRemoved
	if h.t.Failed() {
		h.report.Outcome = "failed"
	} else {
		h.report.Outcome = "passed"
	}
	report := h.report
	h.mu.Unlock()
	if !filepath.IsAbs(path) {
		h.t.Errorf("HOLLIS_LIVE_REPORT must be absolute")
		return
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		h.t.Errorf("marshal sanitized live report: %v", err)
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		h.t.Errorf("open sanitized live report: %v", err)
		return
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		file.Close()
		h.t.Errorf("write sanitized live report: %v", err)
		return
	}
	if err := file.Sync(); err != nil {
		file.Close()
		h.t.Errorf("sync sanitized live report: %v", err)
		return
	}
	if err := file.Close(); err != nil {
		h.t.Errorf("close sanitized live report: %v", err)
	}
}

func assertModelResponse(t *testing.T, raw, model string) {
	t.Helper()
	var payload map[string]any
	decodeObject(t, raw, &payload)
	if payload["model_requested"] != model {
		t.Fatalf("model_requested=%v want=%s", payload["model_requested"], model)
	}
	used, _ := payload["model_used"].(string)
	if model != "auto" && used != model {
		t.Fatalf("model_used=%v want=%s", payload["model_used"], model)
	}
	if model == "auto" && used != "cloud" && used != "on-device" {
		t.Fatalf("auto model_used=%v, want cloud or on-device", payload["model_used"])
	}
	if text, _ := payload["response"].(string); strings.TrimSpace(text) == "" {
		t.Fatal("model response was empty")
	}
}

func Example_liveCommand() {
	fmt.Println("HOLLIS_LIVE=1 HOLLIS_BIN=/absolute/path/to/hollis go test -tags=hollis_live ./internal/integration -run TestLiveRealSystem -v")
	// Output: HOLLIS_LIVE=1 HOLLIS_BIN=/absolute/path/to/hollis go test -tags=hollis_live ./internal/integration -run TestLiveRealSystem -v
}
