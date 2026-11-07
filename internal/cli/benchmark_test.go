// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/spf13/cobra"
)

type benchmarkFake struct {
	t         *testing.T
	elapsed   time.Duration
	calls     int
	status    runner.LocalStatus
	statusErr error
	failAt    int
	wrongAt   int
	cancelAt  int
	cancel    context.CancelFunc
	before    func()
	noUsage   bool
	blockAt   int
}

func newBenchmarkFake(t *testing.T) *benchmarkFake {
	return &benchmarkFake{t: t, status: runner.LocalStatus{Available: true, ContextSize: 4096}}
}

func (f *benchmarkFake) deps() benchmarkDeps {
	return benchmarkDeps{
		newRunner: func() benchmarkRunner { return f },
		now:       func() time.Time { return time.Unix(0, 0).Add(f.elapsed) },
		header: func(context.Context) benchmarkHeader {
			return benchmarkHeader{HollisVersion: "dev", MacOSVersion: "27.0", MacOSBuild: "test-build", MacModel: "test-model", Chip: "test-chip", Note: "Results vary with system load."}
		},
	}
}

func (f *benchmarkFake) NativeStatus(context.Context) (runner.LocalStatus, error) {
	if f.before != nil {
		f.before()
	}
	f.elapsed += 50 * time.Millisecond
	return f.status, f.statusErr
}

func (f *benchmarkFake) begin(ctx context.Context, model runner.Model) error {
	f.t.Helper()
	if model != runner.ModelLocal {
		f.t.Fatalf("non-local request: %s", model)
	}
	if f.before != nil {
		f.before()
	}
	f.calls++
	f.elapsed += 100 * time.Millisecond
	if f.calls == f.cancelAt {
		f.cancel()
	}
	if f.calls == f.blockAt {
		<-ctx.Done()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if f.calls == f.failAt {
		return &runner.Error{Kind: runner.KindNativeFailed, ExitCode: -1, Err: errors.New("synthetic failure")}
	}
	return nil
}

func (f *benchmarkFake) completion(text string) runner.Completion {
	result := runner.Completion{Model: runner.ModelLocal, Text: text}
	if !f.noUsage {
		result.Usage = &runner.Usage{InputTokens: 10, OutputTokens: 5}
	}
	return result
}

func (f *benchmarkFake) RunComplete(ctx context.Context, model runner.Model, prompt string) (runner.Completion, error) {
	if prompt != "Reply with the single word OK" {
		f.t.Fatal("unexpected short prompt")
	}
	if err := f.begin(ctx, model); err != nil {
		return runner.Completion{}, err
	}
	text := "OK"
	if f.calls == f.wrongAt {
		text = "wrong"
	}
	return f.completion(text), nil
}

func (f *benchmarkFake) Stream(ctx context.Context, model runner.Model, prompt string, emit func(string) error) (runner.Completion, error) {
	if !strings.HasPrefix(prompt, "In about 100 words, explain how a cache") {
		f.t.Fatal("unexpected stream prompt")
	}
	if err := f.begin(ctx, model); err != nil {
		return runner.Completion{}, err
	}
	f.elapsed += 50 * time.Millisecond
	text := strings.TrimSpace(strings.Repeat("cache ", 100))
	if err := emit(text); err != nil {
		return runner.Completion{}, err
	}
	f.elapsed += 50 * time.Millisecond
	return f.completion(text), nil
}

func (f *benchmarkFake) RunSchema(ctx context.Context, model runner.Model, prompt string, schema *runner.Schema) (runner.Completion, error) {
	if prompt != "Return a JSON object with status set to OK and count set to 3." {
		f.t.Fatal("unexpected schema prompt")
	}
	if err := f.begin(ctx, model); err != nil {
		return runner.Completion{}, err
	}
	result := f.completion(`{"status":"OK","count":3}`)
	result.Structured = json.RawMessage(result.Text)
	if err := schema.Validate(result.Structured); err != nil {
		f.t.Fatal(err)
	}
	return result, nil
}

func (f *benchmarkFake) CountTokens(ctx context.Context, request runner.GenerationRequest, reserve int) (runner.TokenCount, error) {
	if !strings.HasPrefix(request.Prompt, "In about 100 words, explain how a cache") || reserve != 0 || request.Instructions != "" || len(request.History) != 0 {
		f.t.Fatal("unexpected count request")
	}
	if err := f.begin(ctx, runner.ModelLocal); err != nil {
		return runner.TokenCount{}, err
	}
	return runner.TokenCount{Tokens: 25, ContextSize: 4096, Fits: true}, nil
}

func executeBenchmarkFake(t *testing.T, ctx context.Context, f *benchmarkFake, args ...string) (string, string, error) {
	t.Helper()
	flags := &rootFlags{resultSchema: commandResultSchemas["hollis benchmark"]}
	cmd := newBenchmarkCmd(flags, f.deps())
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.Flags().BoolVar(&flags.asJSON, "json", false, "")
	cmd.Flags().BoolVar(&flags.agent, "agent", false, "")
	cmd.Flags().BoolVar(&flags.quiet, "quiet", false, "")
	cmd.Flags().StringVar(&flags.selectFields, "select", "", "")
	cmd.PreRunE = func(*cobra.Command, []string) error {
		flags.asJSON = flags.asJSON || flags.agent
		_, err := selectionPaths(flags.selectFields, flags.resultSchema)
		return err
	}
	var out, notes bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&notes)
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	err := executeCommand(cmd, flags)
	return out.String(), notes.String(), err
}

func TestBenchmarkStatistics(t *testing.T) {
	for _, tc := range []struct {
		values []float64
		want   *benchmarkStats
	}{
		{nil, nil},
		{[]float64{7}, &benchmarkStats{Median: 7, P95: 7, Min: 7, Max: 7}},
		{[]float64{4, 1, 3, 2}, &benchmarkStats{Median: 2.5, P95: 4, Min: 1, Max: 4}},
		{[]float64{3, 1, 2}, &benchmarkStats{Median: 2, P95: 3, Min: 1, Max: 3}},
	} {
		before := append([]float64(nil), tc.values...)
		if got := benchmarkStatistics(tc.values); !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(tc.values, before) {
			t.Fatalf("values=%v got=%+v want=%+v", tc.values, got, tc.want)
		}
	}
	values := make([]float64, 20)
	for i := range values {
		values[i] = float64(i + 1)
	}
	if got := benchmarkStatistics(values); got.P95 != 19 || got.Median != 10.5 {
		t.Fatalf("nearest-rank: %+v", got)
	}
}

func TestBenchmarkRefusesInvalidOptionsWithoutCalls(t *testing.T) {
	for _, args := range [][]string{
		{"--model", "cloud"}, {"--model", "cloud-pro"}, {"--model", "chatgpt"},
		{"--model", "on-device"}, {"--model", "auto"}, {"--model", ""},
		{"--runs", "0"}, {"--runs", "-1"}, {"--runs", "21"}, {"user content"},
	} {
		f := newBenchmarkFake(t)
		out, _, err := executeBenchmarkFake(t, t.Context(), f, args...)
		if ExitCode(err) != 2 || f.calls != 0 || f.elapsed != 0 || out != "" {
			t.Fatalf("%v: err=%v calls=%d out=%q", args, err, f.calls, out)
		}
	}
}

func TestBenchmarkLocalUnavailable(t *testing.T) {
	for _, platformError := range []bool{false, true} {
		f := newBenchmarkFake(t)
		f.status = runner.LocalStatus{Reason: "Apple Intelligence is disabled"}
		if platformError {
			f.statusErr = &runner.Error{Kind: runner.KindLocalUnavailable, ExitCode: -1, Err: errors.New("native local requires macOS 27 or later")}
		}
		out, _, err := executeBenchmarkFake(t, t.Context(), f, "--json")
		if ExitCode(err) != 5 || f.calls != 0 || !strings.Contains(out, `"code":"local_unavailable"`) || !ErrorReported(err) {
			t.Fatalf("err=%v calls=%d out=%s", err, f.calls, out)
		}
	}
}

func TestBenchmarkCountAndMeasuredUsage(t *testing.T) {
	// No model/timeout environment value can change this command's route.
	t.Setenv("HOLLIS_MODEL", "chatgpt")
	t.Setenv("HOLLIS_TIMEOUT", "invalid")
	for _, runs := range []int{1, 3, 5, 20} {
		f := newBenchmarkFake(t)
		report, err := runBenchmark(t.Context(), runs, f.deps())
		if err != nil || f.calls != 1+4*runs || report.PlannedRequests != f.calls || report.AttemptedRequests != f.calls {
			t.Fatalf("runs=%d report=%+v calls=%d err=%v", runs, report, f.calls, err)
		}
		if *report.HelperStartMS != 50 || report.FirstCall.WallMS != 100 || report.Cases[0].WallMS.Median != 100 ||
			report.Cases[1].WallMS.Median != 200 || report.Cases[1].FirstTextMS.Median != 150 ||
			report.Cases[0].OutputTokensPerSecond.Median != 50 || report.Cases[3].Samples[0].TokenCount == nil ||
			report.Cases[3].OutputTokensPerSecond != nil {
			t.Fatalf("incorrect clock/usage measurement: %+v", report)
		}
	}
	f := newBenchmarkFake(t)
	f.noUsage = true
	report, err := runBenchmark(t.Context(), 1, f.deps())
	if err != nil || report.Cases[1].OutputTokensPerSecond != nil || report.FirstCall.Usage != nil {
		t.Fatalf("invented usage: %+v %v", report, err)
	}
}

func TestBenchmarkUpfrontCount(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{{nil, "21 Local requests (16 generations, 5 token counts)"}, {[]string{"--runs", "3"}, "13 Local requests (10 generations, 3 token counts)"}} {
		f := newBenchmarkFake(t)
		// The dependency runs only after the notice has been written.
		flags := &rootFlags{}
		cmd := newBenchmarkCmd(flags, f.deps())
		var notes bytes.Buffer
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&notes)
		cmd.SetArgs(append([]string{}, tc.args...))
		f.before = func() {
			if !strings.Contains(notes.String(), tc.want) {
				t.Fatal("request started before bounded count was printed")
			}
		}
		if err := cmd.Execute(); err != nil || !strings.Contains(notes.String(), tc.want) {
			t.Fatalf("err=%v notes=%s", err, notes.String())
		}
	}
}

func TestBenchmarkCountsWrongAndFailedAnswers(t *testing.T) {
	f := newBenchmarkFake(t)
	f.wrongAt, f.failAt = 2, 3
	out, _, err := executeBenchmarkFake(t, t.Context(), f, "--runs", "3", "--json")
	var report benchmarkReport
	if json.Unmarshal([]byte(out), &report) != nil || ExitCode(err) != 5 || !ErrorReported(err) || report.Cases[0].Attempted != 3 || report.Cases[0].Succeeded != 1 {
		t.Fatalf("err=%v report=%s", err, out)
	}
	if report.Cases[0].Samples[0].ErrorCode != "wrong_answer" || report.Cases[0].Samples[1].ErrorCode != "native_failed" || report.Cases[0].Samples[0].Usage != nil || report.Cases[0].WallMS.Median != 100 {
		t.Fatalf("failure was hidden: %s", out)
	}
}

func TestBenchmarkPartialCancellation(t *testing.T) {
	for _, mode := range []string{"human", "--json", "--agent"} {
		ctx, cancel := context.WithCancel(t.Context())
		f := newBenchmarkFake(t)
		f.cancelAt, f.cancel = 3, cancel
		args := []string{"--runs", "3"}
		if mode != "human" {
			args = append(args, mode)
		}
		out, _, err := executeBenchmarkFake(t, ctx, f, args...)
		cancel()
		if ExitCode(err) != 130 || f.calls != 3 || !ErrorReported(err) {
			t.Fatalf("mode=%s err=%v calls=%d", mode, err, f.calls)
		}
		if mode == "human" {
			if !strings.Contains(out, "Interrupted; partial results") || !strings.Contains(out, "Requests attempted: 3/13") {
				t.Fatal(out)
			}
		} else {
			var value map[string]any
			if err := json.Unmarshal([]byte(out), &value); err != nil {
				t.Fatal(err)
			}
			if mode == "--agent" {
				value = value["results"].(map[string]any)
			}
			if value["interrupted"] != true || value["attempted_requests"] != float64(3) || len(value["cases"].([]any)) != 4 {
				t.Fatal(out)
			}
		}
	}
}

func TestBenchmarkJSONAndAgentShape(t *testing.T) {
	for _, mode := range []string{"--json", "--agent"} {
		f := newBenchmarkFake(t)
		out, notes, err := executeBenchmarkFake(t, t.Context(), f, "--runs", "1", mode)
		var value map[string]any
		if err != nil || json.Unmarshal([]byte(out), &value) != nil || !strings.Contains(notes, "5 Local requests") {
			t.Fatalf("err=%v out=%s notes=%s", err, out, notes)
		}
		if mode == "--agent" {
			if value["meta"].(map[string]any)["schema_version"] != "2" {
				t.Fatal(out)
			}
			value = value["results"].(map[string]any)
		}
		for _, name := range []string{"schema_version", "model", "header", "runs", "planned_requests", "attempted_requests", "interrupted", "percentile_method", "helper_start_ms", "first_call", "cases"} {
			if _, ok := value[name]; !ok {
				t.Fatalf("missing field %s: %s", name, out)
			}
		}
		if value["schema_version"] != "1" || value["model"] != "local" || value["percentile_method"] != "nearest-rank" || strings.Contains(out, "cache cache") {
			t.Fatal(out)
		}
	}
	f := newBenchmarkFake(t)
	out, _, err := executeBenchmarkFake(t, t.Context(), f, "--runs", "1", "--json", "--select", "planned_requests,cases.succeeded")
	if err != nil || !strings.Contains(out, `"planned_requests":5`) || strings.Contains(out, `"header"`) {
		t.Fatalf("selection: %s %v", out, err)
	}
}

func TestBenchmarkModelShorthand(t *testing.T) {
	f := newBenchmarkFake(t)
	out, _, err := executeBenchmarkFake(t, t.Context(), f, "-m", "local", "--runs", "1", "--json")
	var report benchmarkReport
	if err != nil || json.Unmarshal([]byte(out), &report) != nil || report.Model != "local" ||
		report.AttemptedRequests != 5 || f.calls != 5 {
		t.Fatalf("err=%v calls=%d out=%s", err, f.calls, out)
	}
	f = newBenchmarkFake(t)
	_, _, err = executeBenchmarkFake(t, t.Context(), f, "-m", "cloud")
	if ExitCode(err) != 2 || f.calls != 0 {
		t.Fatalf("non-Local shorthand err=%v calls=%d", err, f.calls)
	}
}

func TestBenchmarkHelpAndAgentGoldens(t *testing.T) {
	out, err := runGoldenCommand(t, "benchmark", "--help")
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "benchmark_help.txt", out)
	root := NewRootCmd(func() runner.Runner { t.Fatal("help constructed a routed runner"); return nil })
	for _, command := range buildAgentContext(root).Commands {
		if command.Name == "benchmark" {
			data, err := json.MarshalIndent(command, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			compareGolden(t, "benchmark_agent_context.json", append(data, '\n'))
			return
		}
	}
	t.Fatal("benchmark missing from agent-context")
}

func TestBenchmarkOutputGoldens(t *testing.T) {
	for _, mode := range []string{"human", "--json"} {
		f := newBenchmarkFake(t)
		args := []string{"--runs", "1"}
		if mode == "--json" {
			args = append(args, mode)
		}
		out, _, err := executeBenchmarkFake(t, t.Context(), f, args...)
		if err != nil {
			t.Fatal(err)
		}
		name := "benchmark_human.txt"
		if mode == "--json" {
			name = "benchmark_json.json"
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, []byte(out), "", "  "); err != nil {
				t.Fatal(err)
			}
			out = pretty.String()
		}
		compareGolden(t, name, []byte(out))
	}
}

func TestBenchmarkInvalidHelpIsUsage(t *testing.T) {
	for _, args := range [][]string{{"benchmark", "--model", "cloud", "--help"}, {"benchmark", "-m", "cloud", "--help"}, {"benchmark", "--runs", "21", "--help"}} {
		out, err := runGoldenCommand(t, args...)
		if ExitCode(err) != 2 || len(out) != 0 {
			t.Fatalf("args=%v err=%v out=%s", args, err, out)
		}
	}
}

func TestBenchmarkSignalHelper(t *testing.T) {
	if os.Getenv("HOLLIS_BENCHMARK_SIGNAL_HELPER") != "1" {
		return
	}
	f := newBenchmarkFake(t)
	f.blockAt = 2
	f.before = func() {
		if f.calls == 1 {
			if err := os.WriteFile(os.Getenv("HOLLIS_BENCHMARK_READY"), []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	out, _, err := executeBenchmarkFake(t, t.Context(), f, "--runs", "3", "--json")
	_, _ = os.Stdout.WriteString(out)
	os.Exit(ExitCode(err))
}

func TestBenchmarkCtrlCPrintsPartialResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBenchmarkSignalHelper$")
	cmd.Env = append(os.Environ(), "HOLLIS_BENCHMARK_SIGNAL_HELPER=1", "HOLLIS_BENCHMARK_READY="+ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("child ended before ready: %v %s", err, out.String())
		case <-ctx.Done():
			t.Fatal("child never became ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var exit *exec.ExitError
		var report benchmarkReport
		if !errors.As(err, &exit) || exit.ExitCode() != 130 || json.Unmarshal(out.Bytes(), &report) != nil || !report.Interrupted || report.AttemptedRequests != 2 {
			t.Fatalf("err=%v out=%s", err, out.String())
		}
	case <-ctx.Done():
		t.Fatal("Ctrl-C did not stop benchmark")
	}
}
