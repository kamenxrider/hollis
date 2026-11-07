// Copyright 2026 Hollis contributors. Licensed under Apache-2.0. See LICENSE.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kamenxrider/hollis/internal/shortcutdiagnostic"
	"github.com/kamenxrider/hollis/internal/subproc"
)

const (
	RateLimitedMessage    = "Apple is rate-limiting cloud requests from this Mac. Wait before trying again (this can last hours)."
	ContentRefusedMessage = "Apple declined this request. Rephrasing may help; retrying will not."
	AccessRequiredMessage = "Cloud Pro is not available for this account."
	appleTextLimit        = 2048
)

// ShortcutFailureInfo contains identifiers only, never unified-log messages,
// request identifiers, process identifiers, prompts or model output.
type ShortcutFailureInfo struct {
	Domain string   `json:"domain,omitempty"`
	Code   int      `json:"code,omitempty"`
	Keys   []string `json:"keys,omitempty"`
}

var domainCode = regexp.MustCompile(`WFAskLLMActionErrorDomain(?:\s+Code=|,\s*code:\s*)([0-9]+)\b`)
var diagnosticPath = regexp.MustCompile(`(?:/Users/|/private/|/var/|/tmp/)[^\s"'<>;,}]+`)
var diagnosticSecret = regexp.MustCompile(`(?i)(?:bearer\s+\S+|(?:token|password|secret|api[_-]?key)\s*[=:]\s*[^\s;,}]+)`)

func sanitizeAppleText(value, prompt string) string {
	// Remove input before bounding, so an input crossing the excerpt boundary
	// cannot leave its prefix in an error. Also remove individual prompt lines.
	if prompt != "" {
		value = strings.ReplaceAll(value, prompt, "[request redacted]")
		for _, line := range strings.Split(prompt, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				value = strings.ReplaceAll(value, line, "[request redacted]")
			}
		}
	}
	value = diagnosticPath.ReplaceAllString(value, "[path redacted]")
	value = diagnosticSecret.ReplaceAllString(value, "[credential redacted]")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(value, "�"))
	value = strings.TrimSpace(value)
	if len(value) > appleTextLimit {
		marker := " [truncated] "
		if strings.Contains(value, "[stderr truncated]") {
			marker = " [stderr truncated] "
		}
		value = string(bytes.ToValidUTF8([]byte(value[:appleTextLimit/2]), nil)) + marker +
			string(bytes.ToValidUTF8([]byte(value[len(value)-appleTextLimit/2:]), nil))
	}
	return value
}

// PublicShortcutMessage includes only the already-sanitized child excerpts.
// Err is deliberately not used: arbitrary nested transport errors may hold paths.
func (e *Error) PublicShortcutMessage() string {
	base := shortcutdiagnostic.FailedMessage
	switch e.Kind {
	case KindRateLimited:
		base = RateLimitedMessage
	case KindContentRefused:
		base = ContentRefusedMessage
	case KindAccessRequired:
		base = AccessRequiredMessage
	case KindRequestDeclined:
		base = shortcutdiagnostic.DeclinedMessage
	}
	if !e.appleSanitized {
		return base
	}
	for _, stream := range []struct{ name, text string }{{"stderr", e.Stderr}, {"stdout", e.Stdout}} {
		if stream.text != "" {
			base += fmt.Sprintf(" Apple %s: %s", stream.name, stream.text)
		}
	}
	return base
}

func (e *Error) ShortcutDetails() map[string]any {
	if !e.appleSanitized {
		return nil
	}
	details := map[string]any{}
	if e.Stderr != "" {
		details["apple_stderr"] = e.Stderr
	}
	if e.Stdout != "" {
		details["apple_stdout"] = e.Stdout
	}
	if e.ShortcutFailure != nil {
		details["shortcut_failure"] = e.ShortcutFailure
	}
	return details
}

func (r *ShortcutRunner) classifyFailure(ctx context.Context, e *Error, start, end time.Time, prompt, stdout, stderr string) {
	// Text always remains available if the bounded log check cannot classify.
	kind := e.Kind
	if isRateLimited(stderr) || isAppleRateLimitOutput(stdout) {
		kind = KindRateLimited
	}
	var info ShortcutFailureInfo
	if r.failureLog != nil {
		info = r.failureLog(ctx, start, end)
	} else if runtime.GOOS == "darwin" && r.ShortcutsPath == DefaultShortcutsPath {
		info = readShortcutFailureLog(ctx, start, end, "/usr/bin/log", 5*time.Second)
	}
	switch info.Code {
	case 1508:
		kind = KindRateLimited
	case 900:
		kind = KindContentRefused
	case 1511:
		kind = KindAccessRequired
	}
	e.Kind = kind
	e.Stderr = sanitizeAppleText(stderr, prompt)
	e.Stdout = sanitizeAppleText(stdout, prompt)
	e.appleSanitized = true
	if info.Domain != "" {
		e.ShortcutFailure = &info
	}
	if kind == KindNoOutput {
		e.Err = fmt.Errorf("%s", sanitizeAppleText(e.Err.Error(), prompt))
	} else {
		e.Err = fmt.Errorf("%s", e.PublicShortcutMessage())
	}
}

func readShortcutFailureLog(ctx context.Context, start, end time.Time, command string, budget time.Duration) ShortcutFailureInfo {
	result, err := subproc.Run(ctx, subproc.Spec{
		Name: command,
		Args: []string{"show", "--style", "ndjson", "--start", start.Add(-time.Second).Local().Format("2006-01-02 15:04:05"),
			"--end", end.Add(2 * time.Second).Local().Format("2006-01-02 15:04:05"),
			"--predicate", `(process == "BackgroundShortcutRunner") OR (eventMessage CONTAINS "WFAskLLMActionErrorDomain") OR (subsystem CONTAINS "PrivateCloudCompute")`},
		Timeout: budget, StdoutCap: 1 << 20, StderrCap: -1,
	})
	if err != nil || result.WaitErr != nil || result.StdoutTruncated {
		return ShortcutFailureInfo{}
	}
	return parseShortcutFailureLogWindow(result.Stdout, start, end)
}

func parseShortcutFailureLog(data []byte) ShortcutFailureInfo {
	return parseShortcutFailureLogWindow(data, time.Time{}, time.Time{})
}

func parseShortcutFailureLogWindow(data []byte, start, end time.Time) ShortcutFailureInfo {
	pids := map[int]bool{}
	codes := map[int]bool{}
	keys := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var row struct {
			ProcessID        int    `json:"processID"`
			ProcessImagePath string `json:"processImagePath"`
			EventMessage     string `json:"eventMessage"`
			Timestamp        string `json:"timestamp"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			return ShortcutFailureInfo{}
		}
		if filepath.Base(row.ProcessImagePath) != "BackgroundShortcutRunner" || row.ProcessID <= 0 {
			continue
		}
		if !start.IsZero() {
			at, err := time.Parse("2006-01-02 15:04:05.999999999-0700", row.Timestamp)
			if err != nil {
				at, err = time.Parse(time.RFC3339Nano, row.Timestamp)
			}
			if err != nil {
				return ShortcutFailureInfo{}
			}
			// The wider query catches buffered logging, but only timestamps
			// inside this invocation can establish its runner identity. A
			// serialized neighbor is not an overlapping run.
			if at.Before(start) || at.After(end) {
				continue
			}
		}
		pids[row.ProcessID] = true
		for _, match := range domainCode.FindAllStringSubmatch(row.EventMessage, -1) {
			code, _ := strconv.Atoi(match[1])
			codes[code] = true
		}
		for _, key := range []string{"deniedDueToUserDeviceRateLimit", "PrivateCloudComputeErrorRetryAfterDate", "AppleIntelligenceRetryAfterDate", "WFModelRejectionCauseErrorKey", "iCloudPlusRequired"} {
			if strings.Contains(row.EventMessage, key) {
				keys[key] = true
			}
		}
	}
	// Multiple runner processes or conflicting codes cannot be attributed to
	// this request. Do not guess from global PCC noise or another app's errors.
	if scanner.Err() != nil || len(pids) != 1 || len(codes) != 1 {
		return ShortcutFailureInfo{}
	}
	info := ShortcutFailureInfo{Domain: "WFAskLLMActionErrorDomain"}
	for code := range codes {
		info.Code = code
	}
	for _, key := range []string{"deniedDueToUserDeviceRateLimit", "PrivateCloudComputeErrorRetryAfterDate", "AppleIntelligenceRetryAfterDate", "WFModelRejectionCauseErrorKey", "iCloudPlusRequired"} {
		if keys[key] {
			info.Keys = append(info.Keys, key)
		}
	}
	return info
}
