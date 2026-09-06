// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package batch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	// MaxResultContentBytes bounds one model response before JSON encoding.
	MaxResultContentBytes = 16 << 20
	maxManifestBytes      = 8 << 20
	// A JSON string can expand each input byte to a six-byte escape. Leave room
	// for the fixed identity metadata while keeping corrupt reads bounded.
	maxResultEnvelopeBytes = 6*MaxResultContentBytes + (1 << 20)
	resultSchemaVersion    = 1
)

// LocalStore persists batch state on the local filesystem. A single instance
// tracks which job locks it owns so Save and CommitResult cannot be used
// outside the exclusive-lock protocol.
type LocalStore struct {
	mu    sync.Mutex
	locks map[string]*localLock
}

type localLock struct {
	store   *LocalStore
	jobPath string
	file    *os.File
	once    sync.Once
	err     error
}

type resultEnvelope struct {
	SchemaVersion int              `json:"schema_version"`
	Request       ExecutionRequest `json:"request"`
	Result        ResultRef        `json:"result"`
	Content       string           `json:"content"`
}

// NewLocalStore returns an empty local durable store.
func NewLocalStore() *LocalStore {
	return &LocalStore{locks: make(map[string]*localLock)}
}

// Create validates and durably creates a new manifest without replacing any
// existing path. Any output directory created here is private; a pre-existing
// output directory must already be private and owned by the current user.
func (s *LocalStore) Create(ctx context.Context, jobPath string, job *Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStoredJob(jobPath, job); err != nil {
		return err
	}
	if err := ensureParentDirectory(jobPath); err != nil {
		return fmt.Errorf("prepare job directory: %w", err)
	}
	if err := createPrivateOutputDirectory(job.OutputDir); err != nil {
		return err
	}
	data, err := marshalManifest(job)
	if err != nil {
		return err
	}
	if err := publishNewFile(jobPath, data); err != nil {
		return fmt.Errorf("create batch manifest: %w", err)
	}
	return nil
}

// Lock acquires a non-blocking OS-backed exclusive lock. The stable lock file
// remains in place after unlock, avoiding unlink-and-recreate races; the kernel
// releases the lock if the process exits.
func (s *LocalStore) Lock(ctx context.Context, jobPath string) (Lock, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	jobPath, err := canonicalStorePath(jobPath)
	if err != nil {
		return nil, err
	}
	if err := ensureParentDirectory(jobPath); err != nil {
		return nil, fmt.Errorf("prepare lock directory: %w", err)
	}
	if err := requireStableLockParent(ctx, filepath.Dir(jobPath)); err != nil {
		return nil, err
	}
	lockPath := jobPath + ".lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	closeWith := func(cause error) (Lock, error) {
		return nil, errors.Join(cause, file.Close())
	}
	info, err := file.Stat()
	if err != nil {
		return closeWith(fmt.Errorf("inspect lock file: %w", err))
	}
	if err := requirePrivateOwnedRegular(info, "lock file"); err != nil {
		return closeWith(err)
	}
	if err := requireSafeLockACL(ctx, lockPath, "lock file"); err != nil {
		return closeWith(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return closeWith(errors.New("batch job is already locked by another process"))
		}
		return closeWith(fmt.Errorf("acquire batch lock: %w", err))
	}
	// Flock protects the open inode, not the pathname. Recheck the full path
	// after admission so a pathname replacement can never be registered as an
	// owned lock.
	if err := requireStableLockParent(ctx, filepath.Dir(jobPath)); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return closeWith(err)
	}
	current, err := os.Lstat(lockPath)
	if err != nil || !os.SameFile(info, current) {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if err != nil {
			return closeWith(fmt.Errorf("reinspect lock file: %w", err))
		}
		return closeWith(errors.New("batch lock path changed during acquisition"))
	}
	if err := requireSafeLockACL(ctx, lockPath, "lock file"); err != nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return closeWith(err)
	}

	owned := &localLock{store: s, jobPath: jobPath, file: file}
	s.mu.Lock()
	if _, exists := s.locks[jobPath]; exists {
		s.mu.Unlock()
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return closeWith(errors.New("batch job is already locked by this store"))
	}
	s.locks[jobPath] = owned
	s.mu.Unlock()
	return owned, nil
}

func (l *localLock) Unlock() error {
	l.once.Do(func() {
		l.store.mu.Lock()
		if l.store.locks[l.jobPath] == l {
			delete(l.store.locks, l.jobPath)
		}
		l.store.mu.Unlock()
		l.err = errors.Join(
			syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN),
			l.file.Close(),
		)
	})
	return l.err
}

// Load strictly reads and validates a bounded manifest from a direct regular
// file. O_NONBLOCK prevents a malicious FIFO from hanging the process.
func (s *LocalStore) Load(ctx context.Context, jobPath string) (*Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	jobPath, err := canonicalStorePath(jobPath)
	if err != nil {
		return nil, err
	}
	var job Job
	if err := readStrictJSON(jobPath, maxManifestBytes, &job); err != nil {
		return nil, fmt.Errorf("read batch manifest: %w", err)
	}
	if err := validateStoredJob(jobPath, &job); err != nil {
		return nil, fmt.Errorf("validate stored batch manifest: %w", err)
	}
	return &job, nil
}

// Save atomically replaces only the validated manifest for the currently
// locked job.
func (s *LocalStore) Save(ctx context.Context, jobPath string, job *Job) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	jobPath, err := canonicalStorePath(jobPath)
	if err != nil {
		return err
	}
	if !s.ownsLock(jobPath) {
		return errors.New("save batch manifest requires this store to hold the job lock")
	}
	if err := validateStoredJob(jobPath, job); err != nil {
		return err
	}
	current, err := s.Load(ctx, jobPath)
	if err != nil {
		return err
	}
	if current.ID != job.ID || current.JobPath != job.JobPath {
		return errors.New("refusing to replace an unrelated batch manifest")
	}
	data, err := marshalManifest(job)
	if err != nil {
		return err
	}
	if err := atomicReplaceFile(jobPath, data); err != nil {
		return fmt.Errorf("save batch manifest: %w", err)
	}
	return nil
}

// CommitResult publishes one self-contained result envelope without replacing
// an existing destination. The request must exactly identify the manifest's
// currently running item.
func (s *LocalStore) CommitResult(ctx context.Context, jobPath string, request ExecutionRequest, output ExecutionOutput) (ResultRef, error) {
	if err := ctx.Err(); err != nil {
		return ResultRef{}, err
	}
	jobPath, err := canonicalStorePath(jobPath)
	if err != nil {
		return ResultRef{}, err
	}
	if !s.ownsLock(jobPath) {
		return ResultRef{}, errors.New("commit batch result requires this store to hold the job lock")
	}
	job, err := s.Load(ctx, jobPath)
	if err != nil {
		return ResultRef{}, err
	}
	item, err := runningItemForRequest(job, request)
	if err != nil {
		return ResultRef{}, err
	}
	if len(output.Content) == 0 {
		return ResultRef{}, errors.New("result content must not be empty")
	}
	if len(output.Content) > MaxResultContentBytes {
		return ResultRef{}, fmt.Errorf("result content exceeds the %d-byte limit", MaxResultContentBytes)
	}
	if !utf8.Valid(output.Content) {
		return ResultRef{}, errors.New("result content must be valid UTF-8")
	}
	if output.ModelUsed != request.Model || output.ModelUsed != job.Model {
		return ResultRef{}, errors.New("result model does not match the planned request")
	}
	if err := requirePrivateOutputDirectory(job.OutputDir); err != nil {
		return ResultRef{}, err
	}

	digest := sha256.Sum256(output.Content)
	checksum := hex.EncodeToString(digest[:])
	ref := ResultRef{
		ID:        ResultID(request.RequestID, checksum),
		RequestID: request.RequestID,
		Path:      filepath.Join(job.OutputDir, item.ResultName),
		SHA256:    checksum,
		Bytes:     int64(len(output.Content)),
		ModelUsed: output.ModelUsed,
	}
	envelope := resultEnvelope{
		SchemaVersion: resultSchemaVersion,
		Request:       request,
		Result:        ref,
		Content:       string(output.Content),
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return ResultRef{}, fmt.Errorf("encode batch result: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxResultEnvelopeBytes {
		return ResultRef{}, errors.New("encoded result envelope exceeds its storage limit")
	}
	if err := publishNewFile(ref.Path, data); err != nil {
		return ResultRef{}, fmt.Errorf("publish batch result: %w", err)
	}
	return ref, nil
}

// VerifyResult validates the complete result envelope for an item. Absence is
// the only state reported as found=false without an error; an occupied but
// invalid destination is a collision or corruption error.
func (s *LocalStore) VerifyResult(ctx context.Context, jobPath string, item Item) (ResultRef, bool, error) {
	if err := ctx.Err(); err != nil {
		return ResultRef{}, false, err
	}
	job, err := s.Load(ctx, jobPath)
	if err != nil {
		return ResultRef{}, false, err
	}
	stored, err := findExactItem(job, item)
	if err != nil {
		return ResultRef{}, false, err
	}
	if err := requirePrivateOutputDirectory(job.OutputDir); err != nil {
		return ResultRef{}, false, err
	}
	path := filepath.Join(job.OutputDir, stored.ResultName)
	var envelope resultEnvelope
	if err := readStrictJSON(path, maxResultEnvelopeBytes, &envelope); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ResultRef{}, false, nil
		}
		return ResultRef{}, false, fmt.Errorf("invalid or colliding result destination %q: %w", path, err)
	}
	if err := validateResultEnvelope(job, stored, envelope); err != nil {
		return ResultRef{}, false, fmt.Errorf("invalid or colliding result destination %q: %w", path, err)
	}
	return envelope.Result, true, nil
}

func validateResultEnvelope(job *Job, item *Item, envelope resultEnvelope) error {
	if envelope.SchemaVersion != resultSchemaVersion {
		return fmt.Errorf("unsupported result schema version %d", envelope.SchemaVersion)
	}
	if item.Status != StatusRunning && item.Status != StatusUncertain && item.Status != StatusSucceeded {
		return fmt.Errorf("item status %q cannot own a committed result", item.Status)
	}
	wantRequest := executionRequestFor(job, *item)
	if envelope.Request != wantRequest {
		return errors.New("result request identity does not match the current manifest attempt")
	}
	if len(envelope.Content) == 0 || len(envelope.Content) > MaxResultContentBytes {
		return errors.New("result content size is invalid")
	}
	digest := sha256.Sum256([]byte(envelope.Content))
	checksum := hex.EncodeToString(digest[:])
	wantRef := ResultRef{
		ID:        ResultID(wantRequest.RequestID, checksum),
		RequestID: wantRequest.RequestID,
		Path:      filepath.Join(job.OutputDir, item.ResultName),
		SHA256:    checksum,
		Bytes:     int64(len(envelope.Content)),
		ModelUsed: job.Model,
	}
	if envelope.Result != wantRef {
		return errors.New("result reference or checksum does not match its content")
	}
	if item.Status == StatusSucceeded && (item.Result == nil || *item.Result != envelope.Result) {
		return errors.New("succeeded manifest result does not match the committed envelope")
	}
	return nil
}

func runningItemForRequest(job *Job, request ExecutionRequest) (*Item, error) {
	for index := range job.Items {
		item := &job.Items[index]
		if item.ID != request.ItemID {
			continue
		}
		if item.Status != StatusRunning {
			return nil, fmt.Errorf("item %q is not running", item.Name)
		}
		if request != executionRequestFor(job, *item) {
			return nil, errors.New("execution request does not match the running manifest item")
		}
		return item, nil
	}
	return nil, errors.New("execution request item does not belong to the job")
}

func executionRequestFor(job *Job, item Item) ExecutionRequest {
	return ExecutionRequest{
		JobID:             job.ID,
		ItemID:            item.ID,
		Attempt:           item.Attempts,
		RequestID:         item.CurrentRequestID,
		Model:             job.Model,
		Kind:              item.Kind,
		SourcePath:        item.SourcePath,
		InputSHA256:       item.InputSHA256,
		InstructionSource: job.InstructionSource,
		InstructionSHA256: job.InstructionSHA256,
	}
}

func findExactItem(job *Job, item Item) (*Item, error) {
	for index := range job.Items {
		stored := &job.Items[index]
		if stored.ID != item.ID {
			continue
		}
		if !reflect.DeepEqual(*stored, item) {
			return nil, errors.New("item does not match the stored manifest state")
		}
		return stored, nil
	}
	return nil, errors.New("item does not belong to the stored manifest")
}

func (s *LocalStore) ownsLock(jobPath string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locks[jobPath] != nil
}

func validateStoredJob(jobPath string, job *Job) error {
	canonical, err := canonicalStorePath(jobPath)
	if err != nil {
		return err
	}
	if err := ValidateJob(job); err != nil {
		return fmt.Errorf("validate batch manifest: %w", err)
	}
	if job.JobPath != canonical {
		return fmt.Errorf("manifest job_path %q does not match storage path %q", job.JobPath, canonical)
	}
	return nil
}

func canonicalStorePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("job path must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve job path: %w", err)
	}
	abs = filepath.Clean(abs)
	// Resolve aliases and symlinks in the parent only. The final path must stay
	// unopened here so Load can reject a final symlink with O_NOFOLLOW.
	parent, err := resolvePath(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("resolve job path parent: %w", err)
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func marshalManifest(job *Job) ([]byte, error) {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode batch manifest: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("batch manifest exceeds the %d-byte limit", maxManifestBytes)
	}
	return data, nil
}

func readStrictJSON(path string, maxBytes int, target any) error {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file (%s)", info.Mode().Type())
	}
	if err := requirePrivateOwnedRegular(info, "stored file"); err != nil {
		return err
	}
	if info.Size() < 1 || info.Size() > int64(maxBytes) {
		return fmt.Errorf("file size %d is outside the 1..%d byte limit", info.Size(), maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return err
	}
	if len(data) > maxBytes {
		return fmt.Errorf("file exceeds the %d-byte limit", maxBytes)
	}
	if !utf8.Valid(data) {
		return errors.New("file is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("file contains more than one JSON value")
		}
		return err
	}
	return nil
}

func ensureParentDirectory(path string) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("parent path is not a directory")
	}
	return nil
}

// requireStableLockParent rejects directory chains in which another local
// principal can replace the lock file or one of its ancestor directories.
// Sticky shared directories are safe when the protected child is owned by the
// current user; the lock file itself is checked after it is opened.
func requireStableLockParent(ctx context.Context, parent string) error {
	parent = filepath.Clean(parent)
	childPath := parent
	childInfo, err := os.Lstat(childPath)
	if err != nil {
		return fmt.Errorf("inspect lock directory: %w", err)
	}
	if !childInfo.IsDir() || childInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("lock parent is not a direct directory")
	}
	if !trustedLockDirectoryOwner(childInfo) {
		return fmt.Errorf("lock parent %q is owned by an untrusted local principal", childPath)
	}
	if childInfo.Mode().Perm()&0o022 != 0 && childInfo.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf("lock parent %q is writable by another local principal without sticky protection", childPath)
	}
	if err := requireSafeLockACL(ctx, childPath, "lock parent"); err != nil {
		return err
	}

	for {
		ancestor := filepath.Dir(childPath)
		if ancestor == childPath {
			return nil
		}
		ancestorInfo, err := os.Lstat(ancestor)
		if err != nil {
			return fmt.Errorf("inspect lock ancestor %q: %w", ancestor, err)
		}
		if !ancestorInfo.IsDir() || ancestorInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("lock ancestor %q is not a direct directory", ancestor)
		}
		if !trustedLockDirectoryOwner(ancestorInfo) {
			return fmt.Errorf("lock ancestor %q is owned by an untrusted local principal", ancestor)
		}
		if ancestorInfo.Mode().Perm()&0o022 != 0 {
			if ancestorInfo.Mode()&os.ModeSticky == 0 {
				return fmt.Errorf("lock ancestor %q can replace %q", ancestor, childPath)
			}
			if !ownedByCurrentUser(childInfo) {
				return fmt.Errorf("sticky lock ancestor %q does not protect unowned child %q", ancestor, childPath)
			}
		}
		if err := requireSafeLockACL(ctx, ancestor, "lock ancestor"); err != nil {
			return err
		}
		childPath = ancestor
		childInfo = ancestorInfo
	}
}

const (
	aclInspectionTimeout = 2 * time.Second
	maxACLListingBytes   = 64 << 10
)

// requireSafeLockACL rejects macOS allow ACL entries that let another local
// principal replace a protected directory entry, delete the lock, or change
// its owner or permissions. Read-only allow entries and recognized deny
// entries do not weaken the mode/owner checks and remain supported.
func requireSafeLockACL(ctx context.Context, path, label string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("batch lock ACL inspection requires macOS")
	}
	inspectCtx, cancel := context.WithTimeout(ctx, aclInspectionTimeout)
	defer cancel()

	command := exec.CommandContext(inspectCtx, "/bin/ls", "-ldbe", path)
	command.Env = []string{"LC_ALL=C", "LANG=C"}
	var stdout, stderr cappedCommandOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctxErr := inspectCtx.Err(); ctxErr != nil {
			return fmt.Errorf("inspect %s ACL: %w", label, ctxErr)
		}
		return fmt.Errorf("inspect %s ACL: native ACL query failed", label)
	}
	if stdout.overflow || stderr.overflow {
		return fmt.Errorf("inspect %s ACL: native ACL output exceeded its limit", label)
	}
	if stderr.Len() != 0 {
		return fmt.Errorf("inspect %s ACL: native ACL query returned unexpected diagnostics", label)
	}
	if err := validateMacACLListing(stdout.Bytes()); err != nil {
		return fmt.Errorf("%s ACL is unsafe: %w", label, err)
	}
	return nil
}

type cappedCommandOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (output *cappedCommandOutput) Len() int      { return output.buffer.Len() }
func (output *cappedCommandOutput) Bytes() []byte { return output.buffer.Bytes() }

func (output *cappedCommandOutput) Write(data []byte) (int, error) {
	written := len(data)
	remaining := maxACLListingBytes - output.Len()
	if remaining <= 0 {
		output.overflow = output.overflow || written != 0
		return written, nil
	}
	if len(data) > remaining {
		output.overflow = true
		data = data[:remaining]
	}
	_, _ = output.buffer.Write(data)
	return written, nil
}

func validateMacACLListing(listing []byte) error {
	if len(listing) == 0 || !utf8.Valid(listing) || bytes.IndexByte(listing, 0) >= 0 {
		return errors.New("native ACL query returned invalid output")
	}
	lines := strings.Split(strings.TrimSuffix(string(listing), "\n"), "\n")
	if len(lines) == 0 || len(lines[0]) == 0 || (lines[0][0] != 'd' && lines[0][0] != '-') {
		return errors.New("native ACL query returned an invalid file header")
	}
	for _, line := range lines[1:] {
		if err := validateMacACLEntry(line); err != nil {
			return err
		}
	}
	return nil
}

func validateMacACLEntry(line string) error {
	if len(line) == 0 || (line[0] != ' ' && line[0] != '\t') {
		return errors.New("native ACL query returned an unrecognized entry")
	}
	entry := strings.TrimSpace(line)
	colon := strings.IndexByte(entry, ':')
	if colon <= 0 {
		return errors.New("native ACL query returned an unrecognized entry")
	}
	for _, digit := range entry[:colon] {
		if digit < '0' || digit > '9' {
			return errors.New("native ACL query returned an unrecognized entry")
		}
	}
	fields := strings.Fields(entry[colon+1:])
	decision := -1
	for index, field := range fields {
		if field == "allow" || field == "deny" {
			if decision != -1 {
				return errors.New("native ACL query returned an ambiguous entry")
			}
			decision = index
		}
	}
	if decision <= 0 || decision != len(fields)-2 || fields[decision+1] == "" {
		return errors.New("native ACL query returned an unrecognized entry")
	}
	for _, permission := range strings.Split(fields[decision+1], ",") {
		if !knownACLPermission(permission) {
			return fmt.Errorf("entry contains unknown permission %q", permission)
		}
		if fields[decision] == "allow" && !safeReadOnlyACLPermission(permission) {
			return fmt.Errorf("allow entry grants %q", permission)
		}
	}
	return nil
}

func knownACLPermission(permission string) bool {
	switch permission {
	case "read", "write", "append", "execute", "delete", "list", "search", "add_file", "add_subdirectory", "delete_child",
		"readattr", "writeattr", "readextattr", "writeextattr", "readsecurity", "writesecurity", "chown", "synchronize",
		"file_inherit", "directory_inherit", "limit_inherit", "only_inherit", "inherited":
		return true
	default:
		return false
	}
}

func safeReadOnlyACLPermission(permission string) bool {
	switch permission {
	case "read", "list", "search", "execute", "readattr", "readextattr", "readsecurity", "synchronize",
		"file_inherit", "directory_inherit", "limit_inherit", "only_inherit", "inherited":
		return true
	default:
		return false
	}
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func trustedLockDirectoryOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && trustedLockOwnerID(stat.Uid)
}

func trustedLockOwnerID(uid uint32) bool {
	return uid == 0 || uid == uint32(os.Getuid())
}

func createPrivateOutputDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private output directory: %w", err)
	}
	return requirePrivateOutputDirectory(path)
}

func requirePrivateOutputDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect output directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("output path is not a direct directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("output directory permissions %04o are not private; require 0700 or stricter", info.Mode().Perm())
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Getuid()) {
		return errors.New("output directory is not owned by the current user")
	}
	return nil
}

func requirePrivateOwnedRegular(info os.FileInfo, label string) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", label)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s permissions %04o are not private", label, info.Mode().Perm())
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("%s is not owned by the current user", label)
	}
	return nil
}

func publishNewFile(path string, data []byte) error {
	parent := filepath.Dir(path)
	staged, err := os.CreateTemp(parent, ".hollis-batch-*")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if err := staged.Chmod(0o600); err != nil {
		_ = staged.Close()
		return err
	}
	if _, err := staged.Write(data); err != nil {
		_ = staged.Close()
		return err
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	// Link is the atomic no-clobber publish primitive. It fails if any file,
	// directory, or symlink appeared at the destination after staging.
	if err := os.Link(stagedPath, path); err != nil {
		return err
	}
	if err := os.Remove(stagedPath); err != nil {
		return err
	}
	return syncDirectory(parent)
}

func atomicReplaceFile(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := requirePrivateOwnedRegular(info, "batch manifest"); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	staged, err := os.CreateTemp(parent, ".hollis-batch-*")
	if err != nil {
		return err
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if err := staged.Chmod(0o600); err != nil {
		_ = staged.Close()
		return err
	}
	if _, err := staged.Write(data); err != nil {
		_ = staged.Close()
		return err
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	// Recheck immediately before replacement so an unrelated path is never
	// knowingly overwritten.
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		return errors.New("batch manifest changed while preparing atomic save")
	}
	if err := os.Rename(stagedPath, path); err != nil {
		return err
	}
	return syncDirectory(parent)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
