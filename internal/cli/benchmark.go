// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kamenxrider/hollis/internal/runner"
	"github.com/kamenxrider/hollis/internal/subproc"
	"github.com/spf13/cobra"
)

const benchmarkHelp = `Measure native Local with fixed built-in prompts; no user input, saved chats,
Shortcuts, other model routes or network requests are used.

Runs one first-call short answer separately, then --runs warm attempts each of:
short answer, streamed ~100-word explanation, guided JSON, and token counting
(no generation). Calls are serial, with a 30s timeout per request and no retry.
Each call starts a helper; "warm" means model/OS caches, not a persistent session.

Reports all attempts, including failures: median, nearest-rank p95, min/max wall
time; streamed time-to-first-text; and tokens/s only from measured SDK usage.
Stream success checks length only (70-140 words), not factual quality.
Results vary with system load. Ctrl-C prints partial results and exits 130.
Any failed or wrong answer exits 5; unavailable Local keeps local_unavailable.
--json and --agent return the same measurements without generated answer text.`

// This deliberately excludes the routed runner and all configuration/default
// resolution. The production constructor supplies only runner.NewNative().
type benchmarkRunner interface {
	runner.NativeStatusRunner
	runner.CompleteRunner
	runner.StreamRunner
	runner.SchemaRunner
	runner.TokenCountRunner
}

type benchmarkDeps struct {
	newRunner func() benchmarkRunner
	now       func() time.Time
	header    func(context.Context) benchmarkHeader
}

func defaultBenchmarkDeps() benchmarkDeps {
	return benchmarkDeps{
		newRunner: func() benchmarkRunner { return runner.NewNative() },
		now:       time.Now,
		header:    readBenchmarkHeader,
	}
}

type benchmarkHeader struct {
	HollisVersion string   `json:"hollis_version"`
	MacOSVersion  string   `json:"macos_version"`
	MacOSBuild    string   `json:"macos_build"`
	MacModel      string   `json:"mac_model"`
	Chip          string   `json:"chip"`
	ContextSize   *int     `json:"context_size"`
	LoadAverage1m *float64 `json:"load_average_1m"`
	Note          string   `json:"note"`
}

type benchmarkStats struct {
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

type benchmarkSample struct {
	Run                   int           `json:"run"`
	Success               bool          `json:"success"`
	WallMS                float64       `json:"wall_ms"`
	FirstTextMS           *float64      `json:"first_text_ms"`
	Usage                 *runner.Usage `json:"usage"`
	OutputTokensPerSecond *float64      `json:"output_tokens_per_second"`
	TokenCount            *int          `json:"token_count"`
	ErrorCode             string        `json:"error_code"`
	Error                 string        `json:"error"`
}

type benchmarkCaseResult struct {
	Name                  string            `json:"name"`
	Check                 string            `json:"check"`
	Attempted             int               `json:"attempted"`
	Succeeded             int               `json:"succeeded"`
	WallMS                *benchmarkStats   `json:"wall_ms"`
	FirstTextMS           *benchmarkStats   `json:"first_text_ms"`
	OutputTokensPerSecond *benchmarkStats   `json:"output_tokens_per_second"`
	Samples               []benchmarkSample `json:"samples"`
}

type benchmarkReport struct {
	SchemaVersion     string                `json:"schema_version"`
	Model             string                `json:"model"`
	Header            benchmarkHeader       `json:"header"`
	Runs              int                   `json:"runs"`
	PlannedRequests   int                   `json:"planned_requests"`
	AttemptedRequests int                   `json:"attempted_requests"`
	Interrupted       bool                  `json:"interrupted"`
	PercentileMethod  string                `json:"percentile_method"`
	HelperStartMS     *float64              `json:"helper_start_ms"`
	FirstCall         *benchmarkSample      `json:"first_call"`
	Cases             []benchmarkCaseResult `json:"cases"`
}

func newBenchmarkCmd(flags *rootFlags, deps benchmarkDeps) *cobra.Command {
	var runs int
	var model string
	validate := func(cmd *cobra.Command, args []string) error {
		if err := noExtraArgs("benchmark")(cmd, args); err != nil {
			return err
		}
		if model != "local" {
			return usageErr(errors.New("benchmark supports only --model local; other routes are never called"))
		}
		if runs < 1 || runs > 20 {
			return usageErr(errors.New("--runs must be between 1 and 20"))
		}
		return nil
	}
	cmd := &cobra.Command{
		Use: "benchmark", Short: "Measure native Local speed with fixed prompts",
		Long: benchmarkHelp, Args: validate,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			planned := 1 + 4*runs
			if !flags.quiet {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "This runs %d Local requests (%d generations, %d token counts), plus availability/protocol checks.\nCalls are serial; each request has a 30s timeout. Ctrl-C stops and prints partial results.\n", planned, 1+3*runs, runs); err != nil {
					return err
				}
			}
			report, err := runBenchmark(ctx, runs, deps)
			if err != nil && !report.Interrupted {
				return err
			}
			if flags.asJSON {
				projected, outputErr := projectOutput(report, flags)
				if outputErr != nil {
					return outputErr
				}
				if flags.agent {
					projected = map[string]any{"meta": agentMeta(), "results": projected}
				}
				if outputErr := encodeJSON(cmd.OutOrStdout(), projected); outputErr != nil {
					return outputErr
				}
			} else if outputErr := printBenchmark(cmd.OutOrStdout(), report); outputErr != nil {
				return outputErr
			}
			if report.Interrupted {
				return &reportedError{err: &cliError{code: 130, machineCode: "interrupted", err: errors.New("benchmark interrupted; partial results printed")}}
			}
			failed := report.FirstCall != nil && !report.FirstCall.Success
			for _, result := range report.Cases {
				failed = failed || result.Succeeded != result.Attempted
			}
			if failed {
				return &reportedError{err: executionErr("benchmark_failed", "benchmark contains failed checks; see results")}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&runs, "runs", 5, "Warm runs per case (1-20)")
	cmd.Flags().StringVarP(&model, "model", "m", "local", "Must be local; no other route is used")
	flags.checkFlagsBeforeHelp(cmd, validate)
	return cmd
}

func runBenchmark(ctx context.Context, runs int, deps benchmarkDeps) (benchmarkReport, error) {
	report := benchmarkReport{
		SchemaVersion: "1", Model: "local", Runs: runs, PlannedRequests: 1 + 4*runs,
		PercentileMethod: "nearest-rank", Cases: []benchmarkCaseResult{
			{Name: "short", Check: "exactly OK after trimming whitespace", Samples: []benchmarkSample{}},
			{Name: "stream", Check: "70-140 whitespace-separated words; length only, not factual quality", Samples: []benchmarkSample{}},
			{Name: "schema", Check: "guided object: status=OK, count=3", Samples: []benchmarkSample{}},
			{Name: "count_tokens", Check: "positive measured token count fitting the reported context; no generation", Samples: []benchmarkSample{}},
		},
	}
	report.Header = deps.header(ctx)
	interrupted := func() bool {
		report.Interrupted = ctx.Err() != nil
		return report.Interrupted
	}
	if interrupted() {
		return report, nil
	}
	r := deps.newRunner()
	start := deps.now()
	status, err := r.NativeStatus(ctx)
	elapsed := benchmarkMS(deps.now().Sub(start))
	report.HelperStartMS = &elapsed
	if interrupted() {
		return report, nil
	}
	if err != nil {
		return report, toCLIError(err)
	}
	if !status.Available {
		return report, toCLIError(&runner.Error{Kind: runner.KindLocalUnavailable, ExitCode: -1,
			Err: fmt.Errorf("native local model is unavailable: %s", status.Reason)})
	}
	if status.ContextSize > 0 {
		report.Header.ContextSize = &status.ContextSize
	}
	schema, err := runner.ParseSchema([]byte(`{"type":"object","properties":{"status":{"type":"string","enum":["OK"]},"count":{"type":"integer","minimum":3,"maximum":3}},"required":["status","count"],"additionalProperties":false}`))
	if err != nil {
		return report, err
	}
	first := measureBenchmark(ctx, r, "short", 1, schema, deps.now)
	report.FirstCall = &first
	report.AttemptedRequests++
	for i := range report.Cases {
		for n := 1; n <= runs; n++ {
			if interrupted() {
				return report, nil
			}
			sample := measureBenchmark(ctx, r, report.Cases[i].Name, n, schema, deps.now)
			report.AttemptedRequests++
			report.Cases[i].Samples = append(report.Cases[i].Samples, sample)
			summarizeBenchmarkCase(&report.Cases[i])
		}
	}
	interrupted()
	return report, nil
}

func measureBenchmark(ctx context.Context, r benchmarkRunner, name string, n int, schema *runner.Schema, now func() time.Time) benchmarkSample {
	const shortPrompt = "Reply with the single word OK"
	const streamPrompt = "In about 100 words, explain how a cache helps a computer program run faster. Use plain prose, no headings."
	const schemaPrompt = "Return a JSON object with status set to OK and count set to 3."
	start := now()
	sample := benchmarkSample{Run: n}
	var completion runner.Completion
	var err error
	var wrong string
	switch name {
	case "short":
		completion, err = r.RunComplete(ctx, runner.ModelLocal, shortPrompt)
		if strings.TrimSpace(completion.Text) != "OK" {
			wrong = "expected the single word OK"
		}
	case "stream":
		completion, err = r.Stream(ctx, runner.ModelLocal, streamPrompt, func(delta string) error {
			if sample.FirstTextMS == nil && strings.TrimSpace(delta) != "" {
				elapsed := benchmarkMS(now().Sub(start))
				sample.FirstTextMS = &elapsed
			}
			return ctx.Err()
		})
		words := len(strings.Fields(completion.Text))
		if words < 70 || words > 140 || sample.FirstTextMS == nil {
			wrong = "expected streamed text of 70-140 words"
		}
	case "schema":
		completion, err = r.RunSchema(ctx, runner.ModelLocal, schemaPrompt, schema)
		if schema.Validate(completion.Structured) != nil {
			wrong = "guided answer did not match status=OK, count=3"
		}
	case "count_tokens":
		var count runner.TokenCount
		count, err = r.CountTokens(ctx, runner.GenerationRequest{Prompt: streamPrompt}, 0)
		if err == nil {
			sample.TokenCount = &count.Tokens
		}
		if count.Tokens <= 0 || count.ContextSize <= 0 || !count.Fits {
			wrong = "expected a positive token count fitting the reported context"
		}
	}
	sample.WallMS = benchmarkMS(now().Sub(start))
	if err != nil {
		classified := toCLIError(err)
		sample.ErrorCode, sample.Error = errorCode(classified), classified.Error()
	} else if wrong != "" {
		sample.ErrorCode, sample.Error = "wrong_answer", wrong
	} else {
		sample.Success = true
	}
	// Never invent usage for missing counts, and never report failed/partial
	// output as successful throughput. Rate includes helper and request overhead.
	if sample.Success && completion.Usage != nil {
		sample.Usage = completion.Usage
		if sample.WallMS > 0 {
			rate := float64(completion.Usage.OutputTokens) / (sample.WallMS / 1000)
			sample.OutputTokensPerSecond = &rate
		}
	}
	return sample
}

func benchmarkMS(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// Median averages the middle pair for even N. P95 is nearest-rank:
// sorted[ceil(0.95*N)-1]. Failed attempts remain in wall-time statistics.
func benchmarkStatistics(values []float64) *benchmarkStats {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	median := sorted[n/2]
	if n%2 == 0 {
		median = sorted[n/2-1]/2 + sorted[n/2]/2
	}
	return &benchmarkStats{Median: median, P95: sorted[int(math.Ceil(.95*float64(n)))-1], Min: sorted[0], Max: sorted[n-1]}
}

func summarizeBenchmarkCase(result *benchmarkCaseResult) {
	var walls, firsts, rates []float64
	result.Attempted, result.Succeeded = len(result.Samples), 0
	for _, sample := range result.Samples {
		walls = append(walls, sample.WallMS)
		if sample.Success {
			result.Succeeded++
		}
		if sample.FirstTextMS != nil {
			firsts = append(firsts, *sample.FirstTextMS)
		}
		if sample.OutputTokensPerSecond != nil {
			rates = append(rates, *sample.OutputTokensPerSecond)
		}
	}
	result.WallMS, result.FirstTextMS, result.OutputTokensPerSecond = benchmarkStatistics(walls), benchmarkStatistics(firsts), benchmarkStatistics(rates)
}

func readBenchmarkHeader(ctx context.Context) benchmarkHeader {
	read := func(path string, args ...string) string {
		result, err := subproc.Run(ctx, subproc.Spec{Name: path, Args: args, Timeout: time.Second, StdoutCap: 4096, StderrCap: -1})
		if err != nil || result.WaitErr != nil || !result.Started {
			return "unknown"
		}
		return strings.TrimSpace(string(result.Stdout))
	}
	header := benchmarkHeader{
		HollisVersion: version, MacOSVersion: read("/usr/bin/sw_vers", "-productVersion"),
		MacOSBuild: read("/usr/bin/sw_vers", "-buildVersion"), MacModel: read("/usr/sbin/sysctl", "-n", "hw.model"),
		Chip: read("/usr/sbin/sysctl", "-n", "machdep.cpu.brand_string"),
		Note: "Results vary with system load. Warm runs reuse model/OS caches, but each request starts a helper. Rates include request overhead; tokens are never estimated.",
	}
	load := strings.Fields(read("/usr/sbin/sysctl", "-n", "vm.loadavg"))
	if len(load) >= 2 {
		if value, err := strconv.ParseFloat(load[1], 64); err == nil && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			header.LoadAverage1m = &value
		}
	}
	return header
}

func printBenchmark(out io.Writer, report benchmarkReport) error {
	var text strings.Builder
	h := report.Header
	fmt.Fprintf(&text, "Hollis %s | macOS %s (%s) | %s | %s\n", h.HollisVersion, h.MacOSVersion, h.MacOSBuild, h.MacModel, h.Chip)
	if h.ContextSize != nil {
		fmt.Fprintf(&text, "Local context: %d tokens", *h.ContextSize)
	} else {
		text.WriteString("Local context: unknown")
	}
	if h.LoadAverage1m != nil {
		fmt.Fprintf(&text, " | load (1m): %.2f", *h.LoadAverage1m)
	}
	fmt.Fprintf(&text, "\n%s\n", h.Note)
	if report.HelperStartMS != nil {
		fmt.Fprintf(&text, "Helper start/status: %.1f ms (includes platform/protocol checks)\n", *report.HelperStartMS)
	}
	if report.FirstCall != nil {
		fmt.Fprintf(&text, "First call (short, separate): %.1f ms; success %t\n", report.FirstCall.WallMS, report.FirstCall.Success)
	}
	fmt.Fprintf(&text, "Requests attempted: %d/%d | warm runs/case: %d | p95: nearest-rank\n", report.AttemptedRequests, report.PlannedRequests, report.Runs)
	table := tabwriter.NewWriter(&text, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "\nCASE\tOK/RUN\tMEDIAN ms\tP95 ms\tMIN ms\tMAX ms\tTTFT ms*\tTOK/s*")
	for _, result := range report.Cases {
		stats := []string{"-", "-", "-", "-"}
		if s := result.WallMS; s != nil {
			stats = []string{fmt.Sprintf("%.1f", s.Median), fmt.Sprintf("%.1f", s.P95), fmt.Sprintf("%.1f", s.Min), fmt.Sprintf("%.1f", s.Max)}
		}
		fmt.Fprintf(table, "%s\t%d/%d\t%s\t%s\t%s\t%s\t%s\t%s\n", result.Name, result.Succeeded, result.Attempted, stats[0], stats[1], stats[2], stats[3], benchmarkMedian(result.FirstTextMS), benchmarkMedian(result.OutputTokensPerSecond))
	}
	if err := table.Flush(); err != nil {
		return err
	}
	text.WriteString("* Medians of measured samples only; '-' means unavailable. Wall times include failures.\nStream checks length only (70-140 words), not factual quality. JSON includes every sample.\n")
	failures := func(label string, samples []benchmarkSample) {
		for _, sample := range samples {
			if !sample.Success {
				fmt.Fprintf(&text, "%s run %d: %s: %s\n", label, sample.Run, sample.ErrorCode, sanitizeTerminalText(sample.Error))
			}
		}
	}
	if report.FirstCall != nil {
		failures("first_call", []benchmarkSample{*report.FirstCall})
	}
	for _, result := range report.Cases {
		failures(result.Name, result.Samples)
	}
	if report.Interrupted {
		text.WriteString("Interrupted; partial results above.\n")
	}
	_, err := io.WriteString(out, text.String())
	return err
}

func benchmarkMedian(stats *benchmarkStats) string {
	if stats == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f", stats.Median)
}
