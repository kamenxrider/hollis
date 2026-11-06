// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

// FileName is the configuration file inside the Hollis state directory.
const FileName = "mcp.json"

const (
	SchemaVersion = 1

	MaxServers         = 32
	maxArgs            = 64
	maxArgBytes        = 8 << 10
	maxEnv             = 32
	maxConfigBytes     = 1 << 20
	DefaultCallTimeout = 20000
	DefaultConnTimeout = 10000
	minTimeoutMS       = 1000
	maxCallTimeoutMS   = 120000
	maxConnTimeoutMS   = 60000
)

// Approval modes.
const (
	ApprovalAsk  = "ask"
	ApprovalAuto = "auto"
)

var (
	serverNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	toolNamePattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
	envNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	headerNamePattern = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$")
	pinPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Config is the decoded mcp.json.
type Config struct {
	SchemaVersion int               `json:"schema_version"`
	Servers       map[string]Server `json:"servers"`
}

// Server is one configured server: stdio when Command is set, HTTP when URL
// is set, never both.
type Server struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	EnvFrom map[string]string `json:"env_from,omitempty"`
	Dir     string            `json:"dir,omitempty"`

	URL     string            `json:"url,omitempty"`
	Auth    *Auth             `json:"auth,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	Approval string   `json:"approval,omitempty"`
	Allow    []string `json:"allow,omitempty"`
	// Pins maps an allowlisted tool to the SHA-256 of the definition that
	// was reviewed when it was allowlisted. Automatic execution requires the
	// live definition to still match.
	Pins map[string]string `json:"pins,omitempty"`

	CallTimeoutMS    int `json:"call_timeout_ms,omitempty"`
	ConnectTimeoutMS int `json:"connect_timeout_ms,omitempty"`
}

// Auth names the environment variable holding an HTTP credential. The value
// itself never appears in the file.
type Auth struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	Env  string `json:"env"`
}

// Transport reports "stdio" or "http".
func (s Server) Transport() string {
	if s.URL != "" {
		return "http"
	}
	return "stdio"
}

// Mode reports the effective approval mode.
func (s Server) Mode() string {
	if s.Approval == "" {
		return ApprovalAsk
	}
	return s.Approval
}

// Allowed reports whether tool is in the allowlist by exact name.
func (s Server) Allowed(tool string) bool { return slices.Contains(s.Allow, tool) }

// Names returns the configured server names in sorted order.
func (c Config) Names() []string {
	names := make([]string, 0, len(c.Servers))
	for name := range c.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// secretLikeName is the deny rule for environment names. It is deliberately a
// predictable substring rule on the name, never on the value.
func secretLikeName(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasPrefix(upper, "HOLLIS") {
		return true
	}
	for _, marker := range []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "CREDENTIAL", "AUTH"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// Load reads path. A missing file is an empty configuration.
func Load(path string) (Config, error) {
	empty := Config{SchemaVersion: SchemaVersion, Servers: map[string]Server{}}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return empty, nil
		}
		return empty, configError("open %s: %v", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return empty, configError("stat %s: %v", path, err)
	}
	if !info.Mode().IsRegular() {
		return empty, configError("%s must be a regular file", path)
	}
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return empty, configError("%v", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return empty, configError("protect %s: %v", path, err)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return empty, configError("read %s: %v", path, err)
	}
	if len(raw) > maxConfigBytes {
		return empty, configError("%s is larger than %d bytes", path, maxConfigBytes)
	}
	cfg, err := Parse(raw)
	if err != nil {
		return empty, configError("%s: %s", path, messageOf(err))
	}
	return cfg, nil
}

// Parse decodes and validates one configuration document.
func Parse(raw []byte) (Config, error) {
	var cfg Config
	if !utf8.Valid(raw) {
		return cfg, configError("file is not valid UTF-8")
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return cfg, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, configError("parse: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return cfg, configError("parse: expected exactly one JSON object")
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]Server{}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate checks every field rule and invariant. Messages name the field.
func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		if c.SchemaVersion > SchemaVersion {
			return configError("schema_version %d was written by a newer Hollis; this version reads %d", c.SchemaVersion, SchemaVersion)
		}
		return configError("schema_version must be %d", SchemaVersion)
	}
	if len(c.Servers) > MaxServers {
		return configError("%d servers configured; at most %d are allowed", len(c.Servers), MaxServers)
	}
	for _, name := range c.Names() {
		if err := ValidateServer(name, c.Servers[name]); err != nil {
			return err
		}
	}
	return nil
}

// ValidateServerName checks the approval identity of a server.
func ValidateServerName(name string) error {
	if !serverNamePattern.MatchString(name) {
		return configError("server name %q must match %s", name, serverNamePattern.String())
	}
	return nil
}

// ValidateToolName checks an allowlist entry. The pattern rules out wildcards
// and prefixes.
func ValidateToolName(server, tool string) error {
	if !toolNamePattern.MatchString(tool) {
		return configError("servers.%s.allow: %q is not an exact tool name (letters, digits, '_', '.', '-'; at most 128); wildcards are not supported", server, tool)
	}
	return nil
}

// ValidateServer checks one server entry.
func ValidateServer(name string, s Server) error {
	if err := ValidateServerName(name); err != nil {
		return err
	}
	field := func(key string) string { return "servers." + name + "." + key }
	stdio, http := s.Command != "", s.URL != ""
	if stdio == http {
		return configError("%s: set exactly one of \"command\" (stdio) or \"url\" (HTTP)", "servers."+name)
	}
	if stdio {
		if len(s.Headers) > 0 {
			return configError("%s is only valid with \"url\"", field("headers"))
		}
		if s.Auth != nil {
			return configError("%s is only valid with \"url\"", field("auth"))
		}
		if err := validateCommand(field("command"), s.Command); err != nil {
			return err
		}
		if len(s.Args) > maxArgs {
			return configError("%s has %d entries; at most %d are allowed", field("args"), len(s.Args), maxArgs)
		}
		for i, arg := range s.Args {
			if len(arg) > maxArgBytes || strings.ContainsRune(arg, 0) {
				return configError("%s[%d] must be at most %d bytes with no NUL", field("args"), i, maxArgBytes)
			}
		}
		if len(s.Env)+len(s.EnvFrom) > maxEnv {
			return configError("%s and %s have %d entries together; at most %d are allowed", field("env"), field("env_from"), len(s.Env)+len(s.EnvFrom), maxEnv)
		}
		for _, key := range sortedKeys(s.Env) {
			if !envNamePattern.MatchString(key) {
				return configError("%s: %q is not a valid variable name", field("env"), key)
			}
			if secretLikeName(key) {
				return configError("%s: %q looks like a credential; literal secrets are not allowed in mcp.json, use \"env_from\" to copy it from your environment", field("env"), key)
			}
			if strings.ContainsRune(s.Env[key], 0) {
				return configError("%s.%s must not contain NUL", field("env"), key)
			}
		}
		for _, child := range sortedKeys(s.EnvFrom) {
			parent := s.EnvFrom[child]
			if !envNamePattern.MatchString(child) || !envNamePattern.MatchString(parent) {
				return configError("%s: %q -> %q is not a pair of valid variable names", field("env_from"), child, parent)
			}
			// The runtime scrubber drops these names anyway; refusing them
			// here keeps a configured entry from silently doing nothing.
			if secretLikeName(child) {
				return configError("%s: child name %q matches the credential deny list (TOKEN, KEY, SECRET, PASSWORD, CREDENTIAL, AUTH, HOLLIS*) and would never reach the server; choose a child variable name outside that list", field("env_from"), child)
			}
		}
		if s.Dir != "" && !filepath.IsAbs(s.Dir) {
			return configError("%s must be an absolute path", field("dir"))
		}
	} else {
		if len(s.Args) > 0 || len(s.Env) > 0 || len(s.EnvFrom) > 0 || s.Dir != "" {
			return configError("%s: \"args\", \"env\", \"env_from\" and \"dir\" are only valid with \"command\"", "servers."+name)
		}
		if err := ValidateURL(field("url"), s.URL); err != nil {
			return err
		}
		if s.Auth != nil {
			if err := validateAuth(field("auth"), *s.Auth); err != nil {
				return err
			}
		}
		if err := validateHeaders(field("headers"), s.Headers, s.Auth); err != nil {
			return err
		}
	}
	switch s.Approval {
	case "", ApprovalAsk:
	case ApprovalAuto:
		if len(s.Allow) == 0 {
			return configError("%s is \"auto\" but %s is empty; list the exact tools that may run without asking", field("approval"), field("allow"))
		}
	default:
		return configError("%s must be \"ask\" or \"auto\"", field("approval"))
	}
	seen := map[string]bool{}
	for _, tool := range s.Allow {
		if err := ValidateToolName(name, tool); err != nil {
			return err
		}
		if seen[tool] {
			return configError("%s lists %q twice", field("allow"), tool)
		}
		seen[tool] = true
	}
	for _, tool := range sortedKeys(s.Pins) {
		if !seen[tool] {
			return configError("%s.%s pins a tool that is not in %s", field("pins"), tool, field("allow"))
		}
		if !pinPattern.MatchString(s.Pins[tool]) {
			return configError("%s.%s must be a lowercase SHA-256 hex digest", field("pins"), tool)
		}
	}
	if s.CallTimeoutMS != 0 && (s.CallTimeoutMS < minTimeoutMS || s.CallTimeoutMS > maxCallTimeoutMS) {
		return configError("%s must be between %d and %d", field("call_timeout_ms"), minTimeoutMS, maxCallTimeoutMS)
	}
	if s.ConnectTimeoutMS != 0 && (s.ConnectTimeoutMS < minTimeoutMS || s.ConnectTimeoutMS > maxConnTimeoutMS) {
		return configError("%s must be between %d and %d", field("connect_timeout_ms"), minTimeoutMS, maxConnTimeoutMS)
	}
	return nil
}

func validateCommand(field, command string) error {
	if strings.ContainsRune(command, 0) || strings.TrimSpace(command) != command {
		return configError("%s must be a program path or name with no surrounding spaces or NUL", field)
	}
	if strings.ContainsRune(command, '/') {
		if !filepath.IsAbs(command) {
			return configError("%s must be an absolute path or a bare program name", field)
		}
		return nil
	}
	if strings.ContainsAny(command, " \t\n'\"`$&|;<>()*?[]{}~\\") {
		return configError("%s must be one program name; arguments go in \"args\" and no shell is used", field)
	}
	return nil
}

// ValidateURL enforces the transport rules: https always, http only to a
// loopback host, no userinfo, no fragment.
func ValidateURL(field, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return configError("%s must be an absolute http(s) URL", field)
	}
	if parsed.User != nil {
		return configError("%s must not contain user information; use \"auth\"", field)
	}
	if parsed.Fragment != "" || strings.Contains(raw, "#") {
		return configError("%s must not contain a fragment", field)
	}
	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(parsed.Hostname()) {
			return nil
		}
		return configError("%s: plaintext http is only allowed for loopback; use https", field)
	default:
		return configError("%s must use https (or http for loopback)", field)
	}
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsLoopback()
}

// validateHeaders accepts only non-secret provider settings, not credentials
// or headers owned by the HTTP/MCP transport. Values are never echoed in errors.
func validateHeaders(field string, headers map[string]string, auth *Auth) error {
	if len(headers) > 32 {
		return configError("%s allows at most 32 entries", field)
	}
	seen := map[string]bool{}
	for _, name := range sortedKeys(headers) {
		if !headerNamePattern.MatchString(name) {
			return configError("%s: invalid header name", field)
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			return configError("%s: duplicate header name (case-insensitive)", field)
		}
		seen[lower] = true
		if secretLikeName(name) || strings.Contains(lower, "cookie") || strings.Contains(lower, "session") ||
			(auth != nil && auth.Type == "header" && strings.EqualFold(name, auth.Name)) {
			return configError("%s: credential-like header names are refused; use \"auth\" with an environment variable", field)
		}
		switch lower {
		case "host", "content-type", "content-length", "accept", "accept-encoding", "connection",
			"transfer-encoding", "te", "trailer", "upgrade", "keep-alive", "proxy-connection":
			return configError("%s: transport header is reserved", field)
		}
		if strings.HasPrefix(lower, "mcp-") {
			return configError("%s: MCP protocol headers are reserved", field)
		}
		value := headers[name]
		if len(value) > 8<<10 {
			return configError("%s: each value must be at most 8192 bytes", field)
		}
		for _, b := range []byte(value) {
			if b < 0x20 || b == 0x7f {
				return configError("%s: values must not contain control characters", field)
			}
		}
	}
	return nil
}

func validateAuth(field string, a Auth) error {
	if !envNamePattern.MatchString(a.Env) {
		return configError("%s.env must name an environment variable", field)
	}
	switch a.Type {
	case "bearer":
		if a.Name != "" {
			return configError("%s.name is only valid with type \"header\"", field)
		}
	case "header":
		if !headerNamePattern.MatchString(a.Name) {
			return configError("%s.name must be a valid header name", field)
		}
		if strings.EqualFold(a.Name, "Authorization") {
			return configError("%s.name must not be Authorization; use type \"bearer\"", field)
		}
	default:
		return configError("%s.type must be \"bearer\" or \"header\"", field)
	}
	return nil
}

// rejectDuplicateKeys walks the token stream because encoding/json keeps the
// last duplicate silently, and server names are approval identities.
func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var walk func(path string) error
	walk = func(path string) error {
		token, err := decoder.Token()
		if err != nil {
			return configError("parse: %v", err)
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return configError("parse: %v", err)
				}
				key, _ := keyToken.(string)
				if seen[key] {
					return configError("duplicate key %q in %s", key, displayPath(path))
				}
				seen[key] = true
				if err := walk(path + "." + key); err != nil {
					return err
				}
			}
		case '[':
			for i := 0; decoder.More(); i++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
		_, err = decoder.Token()
		if err != nil {
			return configError("parse: %v", err)
		}
		return nil
	}
	return walk("")
}

func displayPath(path string) string {
	if path == "" {
		return "the top-level object"
	}
	return strings.TrimPrefix(path, ".")
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func configError(format string, args ...any) *Error {
	return newError(CodeConfig, "", "", format, args...)
}

func messageOf(err error) string {
	var mcpErr *Error
	if errors.As(err, &mcpErr) {
		return mcpErr.Message
	}
	return err.Error()
}

// Save writes cfg atomically under the cross-process lock.
func Save(path string, cfg Config) error {
	return Update(path, func(current *Config) error {
		*current = cfg
		return nil
	})
}

// Update performs one locked read-modify-write. The update sees the current
// file contents; nothing is written when it or validation fails.
func Update(path string, update func(*Config) error) error {
	return withLock(path, func() error {
		cfg, err := Load(path)
		if err != nil {
			return err
		}
		if err := update(&cfg); err != nil {
			return err
		}
		if cfg.Servers == nil {
			cfg.Servers = map[string]Server{}
		}
		cfg.SchemaVersion = SchemaVersion
		if err := cfg.Validate(); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return &Error{Code: CodeConfig, Message: "encode MCP configuration", Err: err}
		}
		if err := atomicWrite(path, append(raw, '\n')); err != nil {
			return &Error{Code: CodeConfig, Message: fmt.Sprintf("write MCP configuration: %v", err), Err: err}
		}
		return nil
	})
}

func withLock(path string, fn func() error) error {
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return configError("%v", err)
	}
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return configError("open lock %s: %v", lockPath, err)
	}
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return configError("protect lock %s: %v", lockPath, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return configError("lock %s: %v", path, err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("state path %s must be a real directory, not a symlink", dir)
	}
	return os.Chmod(dir, 0o700)
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".mcp.json-*")
	if err != nil {
		return err
	}
	tempPath := f.Name()
	defer func() {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	tempPath = ""
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
