// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package batch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/kamenxrider/hollis/internal/chat"
	"github.com/kamenxrider/hollis/internal/docinput"
	"github.com/kamenxrider/hollis/internal/runner"
)

const (
	DefaultMaxInstructionBytes int64 = 128 << 10
	DefaultMaxTextInputBytes   int64 = 128 << 10
	DefaultMaxImageInputBytes  int64 = 64 << 20
	MaxPreparedPromptBytes           = chat.MaxRenderedPromptBytes
)

// PlanOptions supplies the immutable inputs for Plan. CreatedAt and byte
// limits are injectable so callers and tests do not depend on wall-clock time
// or unbounded files. Zero limits select the package defaults.
type PlanOptions struct {
	InputDir            string
	OutputDir           string
	JobPath             string
	InstructionSource   string
	Model               runner.Model
	CreatedAt           time.Time
	MaxInstructionBytes int64
	MaxTextInputBytes   int64
	MaxImageInputBytes  int64
}

// Plan creates a deterministic, nonrecursive manifest and makes no provider
// calls. It refuses symlinks, unsafe path overlap, empty inventories, and any
// pre-existing result destination.
func Plan(options PlanOptions) (*Job, error) {
	inputDir, err := existingDirectory(options.InputDir, "input directory")
	if err != nil {
		return nil, err
	}
	outputDir, err := resolvePath(options.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve output directory: %w", err)
	}
	jobPath, err := resolvePath(options.JobPath)
	if err != nil {
		return nil, fmt.Errorf("resolve job path: %w", err)
	}
	instructionSource, err := existingRegularFile(options.InstructionSource, "instruction source")
	if err != nil {
		return nil, err
	}
	if err := validatePathLayout(inputDir, outputDir, jobPath, instructionSource); err != nil {
		return nil, err
	}
	if err := validateConcreteModel(options.Model); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(jobPath); err == nil {
		return nil, fmt.Errorf("job path %q already exists (%s)", jobPath, info.Mode().Type())
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect job path %q: %w", jobPath, err)
	}
	if info, err := os.Lstat(outputDir); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("output path %q is not a directory", outputDir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect output directory %q: %w", outputDir, err)
	}

	instructionLimit, err := selectedLimit(options.MaxInstructionBytes, DefaultMaxInstructionBytes, "instruction")
	if err != nil {
		return nil, err
	}
	instructionHash, instructionBytes, instruction, err := readRegularBounded(instructionSource, instructionLimit, true)
	if err != nil {
		return nil, fmt.Errorf("read instruction source: %w", err)
	}
	if !utf8.Valid(instruction) {
		return nil, errors.New("instruction source must contain valid UTF-8")
	}
	if strings.TrimSpace(string(instruction)) == "" {
		return nil, errors.New("instruction source must not be empty")
	}

	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return nil, fmt.Errorf("read input directory %q: %w", inputDir, err)
	}
	items := make([]Item, 0, len(entries))
	skipped := make([]Skipped, 0)
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(inputDir, name)
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("input entry %q is a symlink; batch inputs must be direct regular files", name)
		}
		if entry.IsDir() {
			skipped = append(skipped, Skipped{Name: name, Reason: "directory (inventory is nonrecursive)"})
			continue
		}
		kind, supported := kindForName(name)
		if !supported {
			skipped = append(skipped, Skipped{Name: name, Reason: "unsupported file type"})
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect input %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("input entry %q is not a regular file", name)
		}
		if sameFile(path, instructionSource) {
			return nil, fmt.Errorf("instruction source %q is also an input item", name)
		}

		limit, err := selectedLimit(options.MaxTextInputBytes, DefaultMaxTextInputBytes, "text input")
		if err != nil {
			return nil, err
		}
		keepContent := true
		if kind == ItemImage {
			limit, err = selectedLimit(options.MaxImageInputBytes, DefaultMaxImageInputBytes, "image input")
			if err != nil {
				return nil, err
			}
			keepContent = false
		}
		hash, size, content, err := readRegularBounded(path, limit, keepContent)
		if err != nil {
			return nil, fmt.Errorf("read input %q: %w", name, err)
		}
		if kind == ItemText && !utf8.Valid(content) {
			return nil, fmt.Errorf("text input %q must contain valid UTF-8", name)
		}
		if kind == ItemText && strings.TrimSpace(string(content)) == "" {
			return nil, fmt.Errorf("text input %q must contain non-empty content", name)
		}
		if size == 0 {
			return nil, fmt.Errorf("input %q must not be empty", name)
		}
		if kind == ItemText {
			_, err := docinput.Prepare(string(instruction), []docinput.Document{{Name: name, Text: string(content)}}, chat.MaxRenderedPromptBytes)
			if err != nil {
				return nil, fmt.Errorf("prepare text input %q: %w", name, err)
			}
		}
		id := stableItemID(name, kind, hash)
		item := Item{
			ID:          id,
			Name:        name,
			SourcePath:  path,
			Kind:        kind,
			InputSHA256: hash,
			InputBytes:  size,
			ResultName:  stableResultName(name, id),
			Status:      StatusPending,
		}
		if _, err := os.Lstat(filepath.Join(outputDir, item.ResultName)); err == nil {
			return nil, fmt.Errorf("result destination for %q already exists", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect result destination for %q: %w", name, err)
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, errors.New("input directory contains no supported regular files")
	}
	if err := validateModelItems(options.Model, items); err != nil {
		return nil, err
	}

	createdAt := options.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	job := &Job{
		SchemaVersion:     SchemaVersion,
		CreatedAt:         createdAt,
		InputDir:          inputDir,
		OutputDir:         outputDir,
		JobPath:           jobPath,
		InstructionSource: instructionSource,
		InstructionSHA256: instructionHash,
		InstructionBytes:  instructionBytes,
		Model:             options.Model,
		Items:             items,
		Skipped:           skipped,
	}
	job.ID = stableJobID(job)
	if err := ValidateJob(job); err != nil {
		return nil, fmt.Errorf("validate planned job: %w", err)
	}
	return job, nil
}

// ValidateJob validates manifest schema and state without reading its sources.
func ValidateJob(job *Job) error {
	if job == nil {
		return errors.New("job is nil")
	}
	if job.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported batch schema version %d", job.SchemaVersion)
	}
	if err := validateConcreteModel(job.Model); err != nil {
		return err
	}
	if job.ID == "" {
		return errors.New("job id is required")
	}
	if job.CreatedAt.IsZero() {
		return errors.New("job created_at is required")
	}
	if !validSHA256(job.InstructionSHA256) {
		return errors.New("instruction_sha256 must be a lowercase SHA-256 digest")
	}
	if job.InstructionBytes < 1 {
		return errors.New("instruction_bytes must be positive")
	}
	if job.InstructionBytes > DefaultMaxInstructionBytes {
		return fmt.Errorf("instruction_bytes exceeds the %d-byte limit", DefaultMaxInstructionBytes)
	}
	if len(job.Items) == 0 {
		return errors.New("job must contain at least one item")
	}
	paths := []struct{ label, path string }{
		{label: "input_dir", path: job.InputDir},
		{label: "output_dir", path: job.OutputDir},
		{label: "job_path", path: job.JobPath},
		{label: "instruction_source", path: job.InstructionSource},
	}
	for _, candidate := range paths {
		if err := validateCanonicalPath(candidate.label, candidate.path); err != nil {
			return err
		}
	}
	if err := validatePathLayout(job.InputDir, job.OutputDir, job.JobPath, job.InstructionSource); err != nil {
		return err
	}

	seenIDs := make(map[string]struct{}, len(job.Items))
	seenNames := make(map[string]struct{}, len(job.Items))
	for index := range job.Items {
		item := &job.Items[index]
		if index > 0 && job.Items[index-1].Name >= item.Name {
			return errors.New("items must be sorted by unique name")
		}
		if err := validateItem(job, item); err != nil {
			return fmt.Errorf("item %q: %w", item.Name, err)
		}
		if _, exists := seenIDs[item.ID]; exists {
			return fmt.Errorf("duplicate item id %q", item.ID)
		}
		seenIDs[item.ID] = struct{}{}
		if _, exists := seenNames[item.Name]; exists {
			return fmt.Errorf("duplicate item name %q", item.Name)
		}
		seenNames[item.Name] = struct{}{}
	}
	if err := validateModelItems(job.Model, job.Items); err != nil {
		return err
	}
	if want := stableJobID(job); job.ID != want {
		return fmt.Errorf("job id %q does not match manifest identity %q", job.ID, want)
	}
	for index := 1; index < len(job.Skipped); index++ {
		if job.Skipped[index-1].Name >= job.Skipped[index].Name {
			return errors.New("skipped entries must be sorted by unique name")
		}
	}
	for _, skipped := range job.Skipped {
		if skipped.Name == "" || skipped.Name != filepath.Base(skipped.Name) || skipped.Reason == "" {
			return errors.New("skipped entries require a base name and reason")
		}
	}
	return nil
}

// RevalidateSources confirms the instruction and every input still match the
// immutable plan. It reads at most the recorded byte count plus one byte from
// each source and does not inspect or overwrite result destinations.
func RevalidateSources(job *Job) error {
	if err := ValidateJob(job); err != nil {
		return err
	}
	hash, size, instruction, err := readRegularBounded(job.InstructionSource, job.InstructionBytes, true)
	if err != nil {
		if strings.Contains(err.Error(), "exceeds") {
			return fmt.Errorf("instruction source changed since planning: %w", err)
		}
		return fmt.Errorf("revalidate instruction source: %w", err)
	}
	if size != job.InstructionBytes || hash != job.InstructionSHA256 {
		return errors.New("instruction source changed since planning")
	}
	if !utf8.Valid(instruction) || strings.TrimSpace(string(instruction)) == "" {
		return errors.New("instruction source is no longer valid non-empty UTF-8")
	}
	for _, item := range job.Items {
		if err := revalidateInput(string(instruction), item); err != nil {
			return err
		}
	}
	return nil
}

// RevalidateItem performs the immediately-before-call source check for one
// item, including the shared instruction. ExecutionRequest repeats both
// hashes so the executor can close the remaining read/dispatch race.
func RevalidateItem(job *Job, item Item) error {
	if err := ValidateJob(job); err != nil {
		return err
	}
	found := false
	for _, planned := range job.Items {
		if planned.ID == item.ID {
			found = planned.Name == item.Name && planned.InputSHA256 == item.InputSHA256
			break
		}
	}
	if !found {
		return errors.New("item does not belong to the validated job")
	}
	hash, size, instruction, err := readRegularBounded(job.InstructionSource, job.InstructionBytes, true)
	if err != nil {
		if strings.Contains(err.Error(), "exceeds") {
			return fmt.Errorf("instruction source changed since planning: %w", err)
		}
		return fmt.Errorf("revalidate instruction source: %w", err)
	}
	if size != job.InstructionBytes || hash != job.InstructionSHA256 ||
		!utf8.Valid(instruction) || strings.TrimSpace(string(instruction)) == "" {
		return errors.New("instruction source changed since planning")
	}
	return revalidateInput(string(instruction), item)
}

// RequestID returns the deterministic identity for a particular provider
// attempt. The attempt number is one-based.
func RequestID(jobID, itemID string, attempt int) string {
	return "req_" + hashParts(jobID, itemID, strconv.Itoa(attempt))[:24]
}

// ResultID binds committed output bytes to the exact provider attempt that
// produced them.
func ResultID(requestID, resultSHA256 string) string {
	return "result_" + hashParts(requestID, resultSHA256)[:24]
}

// Validate checks one invocation's explicit retry and call-budget choices.
func (options InvocationOptions) Validate() error {
	if options.MaxCalls < 1 || options.MaxCalls > MaxCallsPerInvocation {
		return fmt.Errorf("max calls must be between 1 and %d", MaxCallsPerInvocation)
	}
	if options.RetryUncertain && options.SkipUncertain {
		return errors.New("retry uncertain and skip uncertain are mutually exclusive")
	}
	return nil
}

func validateItem(job *Job, item *Item) error {
	if item.Name == "" || item.Name != filepath.Base(item.Name) {
		return errors.New("name must be a base name")
	}
	kind, supported := kindForName(item.Name)
	if !supported || kind != item.Kind {
		return errors.New("name and kind are not a supported text or image input")
	}
	if item.SourcePath != filepath.Join(job.InputDir, item.Name) {
		return errors.New("source_path is outside the planned input directory")
	}
	if !validSHA256(item.InputSHA256) {
		return errors.New("input_sha256 must be a lowercase SHA-256 digest")
	}
	if item.InputBytes < 0 {
		return errors.New("input_bytes must not be negative")
	}
	if item.InputBytes == 0 {
		return errors.New("input_bytes must be positive")
	}
	limit := DefaultMaxTextInputBytes
	if item.Kind == ItemImage {
		limit = DefaultMaxImageInputBytes
	}
	if item.InputBytes > limit {
		return fmt.Errorf("input_bytes exceeds the %d-byte limit", limit)
	}
	if want := stableItemID(item.Name, item.Kind, item.InputSHA256); item.ID != want {
		return fmt.Errorf("id %q does not match item identity %q", item.ID, want)
	}
	if want := stableResultName(item.Name, item.ID); item.ResultName != want {
		return fmt.Errorf("result_name %q does not match stable name %q", item.ResultName, want)
	}
	if item.Attempts < 0 {
		return errors.New("attempts must not be negative")
	}
	switch item.Status {
	case StatusPending:
		if item.Result != nil || item.Failure != "" {
			return errors.New("pending item must not record a result or failure")
		}
	case StatusRunning:
		if item.Attempts < 1 || item.CurrentRequestID == "" {
			return fmt.Errorf("%s item requires an attempt and request id", item.Status)
		}
		if item.Result != nil || item.Failure != "" {
			return errors.New("running item must not record a result or failure")
		}
	case StatusFailed, StatusUncertain:
		if item.Attempts < 1 || item.CurrentRequestID == "" {
			return fmt.Errorf("%s item requires an attempt and request id", item.Status)
		}
		if item.Result != nil || item.Failure == "" {
			return fmt.Errorf("%s item requires a failure kind and no result", item.Status)
		}
	case StatusSucceeded:
		if item.Attempts < 1 || item.CurrentRequestID == "" || item.Result == nil {
			return errors.New("succeeded item requires an attempt, request id, and result")
		}
		if item.Failure != "" {
			return errors.New("succeeded item must not record a failure")
		}
		if err := validateResult(job, item); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown status %q", item.Status)
	}
	if item.CurrentRequestID != "" {
		want := RequestID(job.ID, item.ID, item.Attempts)
		if item.CurrentRequestID != want {
			return fmt.Errorf("current_request_id %q does not match attempt identity %q", item.CurrentRequestID, want)
		}
	}
	return nil
}

func validateResult(job *Job, item *Item) error {
	result := item.Result
	if result.RequestID != item.CurrentRequestID {
		return errors.New("result identity must match the current request")
	}
	if result.Path != filepath.Join(job.OutputDir, item.ResultName) {
		return errors.New("result path does not match the reserved destination")
	}
	if !validSHA256(result.SHA256) || result.Bytes < 1 {
		return errors.New("result checksum or byte count is invalid")
	}
	if want := ResultID(result.RequestID, result.SHA256); result.ID != want {
		return errors.New("result id does not match its request and checksum")
	}
	if result.ModelUsed != job.Model {
		return errors.New("result model_used must match the planned concrete model")
	}
	return nil
}

func validateConcreteModel(model runner.Model) error {
	if model == runner.ModelAuto {
		return errors.New("batch jobs require an explicit concrete model; auto is unavailable")
	}
	if !concreteModel(model) {
		return fmt.Errorf("unsupported batch model %q", model)
	}
	return nil
}

func concreteModel(model runner.Model) bool {
	return slices.Contains(runner.Models, model)
}

func validateModelItems(model runner.Model, items []Item) error {
	if model == runner.ModelOnDevice {
		for _, item := range items {
			if item.Kind == ItemImage {
				return errors.New("on-device batch jobs cannot contain images")
			}
		}
	}
	return nil
}

func validatePathLayout(inputDir, outputDir, jobPath, instructionSource string) error {
	var err error
	inputDir, err = resolvePath(inputDir)
	if err != nil {
		return fmt.Errorf("resolve input directory: %w", err)
	}
	outputDir, err = resolvePath(outputDir)
	if err != nil {
		return fmt.Errorf("resolve output directory: %w", err)
	}
	jobPath, err = resolvePath(jobPath)
	if err != nil {
		return fmt.Errorf("resolve job path: %w", err)
	}
	instructionSource, err = resolvePath(instructionSource)
	if err != nil {
		return fmt.Errorf("resolve instruction source: %w", err)
	}
	if within(inputDir, outputDir) || within(outputDir, inputDir) {
		return errors.New("input and output directories must not overlap")
	}
	if within(jobPath, inputDir) {
		return errors.New("job path must not be inside the input directory")
	}
	if within(jobPath, outputDir) {
		return errors.New("job path must not be inside the output directory")
	}
	if within(instructionSource, inputDir) {
		return errors.New("instruction source must not be inside the input directory")
	}
	if jobPath == instructionSource {
		return errors.New("job path must not overwrite the instruction source")
	}
	return nil
}

func validateCanonicalPath(label, path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("%s must be an absolute clean path", label)
	}
	resolved, err := resolvePath(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", label, err)
	}
	if resolved != path {
		return fmt.Errorf("%s must use its resolved canonical path", label)
	}
	return nil
}

func existingDirectory(path, label string) (string, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("open %s %q: %w", label, path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s %q is not a directory", label, path)
	}
	return resolved, nil
}

func existingRegularFile(path, label string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	initial, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("open %s %q: %w", label, path, err)
	}
	if initial.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	resolved, err := resolvePath(abs)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", fmt.Errorf("open %s %q: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s %q must not be a symlink", label, path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s %q must be a regular file", label, path)
	}
	return resolved, nil
}

func resolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	current := abs
	missing := make([]string, 0)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing parent for %q", abs)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func within(path, directory string) bool {
	relative, err := filepath.Rel(directory, path)
	if err != nil {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func kindForName(name string) (ItemKind, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".md":
		return ItemText, true
	case ".png", ".jpg", ".jpeg":
		return ItemImage, true
	default:
		return "", false
	}
}

func readRegularBounded(path string, maxBytes int64, keepContent bool) (string, int64, []byte, error) {
	if maxBytes < 0 {
		return "", 0, nil, errors.New("byte limit must not be negative")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", 0, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return "", 0, nil, errors.New("source must be a direct regular file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", 0, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return "", 0, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return "", 0, nil, errors.New("opened source must be a direct regular file")
	}

	hash := sha256.New()
	var writer io.Writer = hash
	var content bytes.Buffer
	if keepContent {
		writer = io.MultiWriter(hash, &content)
	}
	read, readErr := io.Copy(writer, io.LimitReader(file, maxBytes))
	var extra [1]byte
	n, probeErr := file.Read(extra[:])
	closeErr := file.Close()
	if readErr != nil {
		return "", 0, nil, readErr
	}
	if n != 0 {
		return "", 0, nil, fmt.Errorf("file exceeds %d-byte limit", maxBytes)
	}
	if probeErr != nil && !errors.Is(probeErr, io.EOF) {
		return "", 0, nil, probeErr
	}
	if closeErr != nil {
		return "", 0, nil, closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), read, content.Bytes(), nil
}

func stableItemID(name string, kind ItemKind, inputHash string) string {
	return "item_" + hashParts(name, string(kind), inputHash)[:24]
}

func stableJobID(job *Job) string {
	parts := []string{
		strconv.Itoa(job.SchemaVersion), job.InputDir, job.OutputDir, job.JobPath,
		job.InstructionSource, job.InstructionSHA256, string(job.Model),
	}
	for _, item := range job.Items {
		parts = append(parts, item.ID)
	}
	return "job_" + hashParts(parts...)[:24]
}

func stableResultName(name, itemID string) string {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	var safe strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(stem) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			safe.WriteRune(r)
			lastDash = false
		case safe.Len() > 0 && !lastDash:
			safe.WriteByte('-')
			lastDash = true
		}
	}
	base := strings.Trim(safe.String(), "-")
	if base == "" {
		base = "item"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	suffix := strings.TrimPrefix(itemID, "item_")
	return base + "-" + suffix[:12] + ".response.json"
}

func hashParts(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = io.WriteString(hash, strconv.Itoa(len(part)))
		_, _ = io.WriteString(hash, ":")
		_, _ = io.WriteString(hash, part)
		_, _ = io.WriteString(hash, ";")
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func selectedLimit(value, fallback int64, label string) (int64, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s byte limit must not be negative", label)
	}
	if value == 0 {
		return fallback, nil
	}
	if value > fallback {
		return 0, fmt.Errorf("%s byte limit must not exceed %d", label, fallback)
	}
	return value, nil
}

func revalidateInput(instruction string, item Item) error {
	keepContent := item.Kind == ItemText
	hash, size, content, err := readRegularBounded(item.SourcePath, item.InputBytes, keepContent)
	if err != nil {
		if strings.Contains(err.Error(), "exceeds") {
			return fmt.Errorf("input %q changed since planning: %w", item.Name, err)
		}
		return fmt.Errorf("revalidate input %q: %w", item.Name, err)
	}
	if size != item.InputBytes || hash != item.InputSHA256 {
		return fmt.Errorf("input %q changed since planning", item.Name)
	}
	if keepContent && (!utf8.Valid(content) || strings.TrimSpace(string(content)) == "") {
		return fmt.Errorf("text input %q is no longer valid non-empty UTF-8", item.Name)
	}
	if keepContent {
		if _, err := docinput.Prepare(instruction, []docinput.Document{{Name: item.Name, Text: string(content)}}, chat.MaxRenderedPromptBytes); err != nil {
			return fmt.Errorf("prepare text input %q: %w", item.Name, err)
		}
	}
	return nil
}

func sameFile(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
