// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package mcp

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kamenxrider/hollis/internal/subproc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// stopGrace is each polite shutdown stage for a stdio child: after stdin
// closes, and again after SIGTERM, before the group is killed.
const stopGrace = 500 * time.Millisecond

// StderrCap is how much of a stdio server's stderr Hollis keeps in memory.
// The bytes are never printed, logged or stored; only the count is reported.
const StderrCap = 8 << 10

// link is one transport's process or network state, owned by Hollis rather
// than by the SDK, so shutdown and failure classification stay here.
type link interface {
	transport() sdk.Transport
	// kill ends the transport immediately: the process group for stdio, every
	// in-flight request for HTTP.
	kill()
	// close ends it politely and is idempotent.
	close()
	facts() linkFacts
}

type linkFacts struct {
	stderrBytes int64
	exited      bool
	exitCode    int
	httpStatus  int
	redirected  bool
	bodyTooBig  bool
	// sendFailed is set when a request never got an HTTP response; tlsFailed
	// narrows that to certificate or handshake failure.
	sendFailed bool
	tlsFailed  bool
}

// stderrRing keeps the last StderrCap bytes and counts all of them.
type stderrRing struct {
	mu    sync.Mutex
	buf   []byte
	total int64
}

func (r *stderrRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total += int64(len(p))
	r.buf = append(r.buf, p...)
	if len(r.buf) > StderrCap {
		r.buf = append(r.buf[:0], r.buf[len(r.buf)-StderrCap:]...)
	}
	return len(p), nil
}

func (r *stderrRing) count() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

type stdioLink struct {
	proc   *subproc.Session
	stderr *stderrRing
	once   sync.Once
}

func startStdio(name string, s Server, parentEnv []string) (*stdioLink, error) {
	env, err := ChildEnv(parentEnv, name, s)
	if err != nil {
		return nil, err
	}
	program, err := resolveCommand(s.Command, env)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Server: name, Message: "cannot start the server: " + startFailureClass(err), Err: err}
	}
	dir := s.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	ring := &stderrRing{}
	proc, err := subproc.Start(subproc.SessionSpec{Name: program, Args: s.Args, Env: env, Dir: dir, StderrSink: ring})
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Server: name, Message: "cannot start the server: " + startFailureClass(err), Err: err}
	}
	return &stdioLink{proc: proc, stderr: ring}, nil
}

func startFailureClass(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "program not found"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	default:
		return "the program could not be executed"
	}
}

func (l *stdioLink) transport() sdk.Transport {
	return &sdk.IOTransport{Reader: l.proc.Stdout(), Writer: l.proc.Stdin(), MaxLineLength: MaxFrameBytes}
}

func (l *stdioLink) kill() { _ = l.proc.Kill() }

func (l *stdioLink) close() {
	l.once.Do(func() { _ = l.proc.Close(stopGrace) })
}

func (l *stdioLink) facts() linkFacts {
	f := linkFacts{stderrBytes: l.stderr.count(), exitCode: -1}
	select {
	case <-l.proc.Exited():
		f.exited, f.exitCode = true, l.proc.ExitCode()
	default:
	}
	return f
}

// errRedirect is returned for every redirect: following one would move the
// request, and any credential, somewhere the user did not configure.
var errRedirect = errors.New("redirect refused")

var errBodyTooLarge = errors.New("response body exceeded the limit")

type httpLink struct {
	endpoint string
	client   *http.Client
	ctx      context.Context
	cancel   context.CancelFunc

	status     atomic.Int64
	redirected atomic.Bool
	bodyTooBig atomic.Bool
	sendFailed atomic.Bool
	tlsFailed  atomic.Bool
}

// errAuthRejected stops every request after the server rejected the
// credential once, so the handshake's fallback cannot send it again.
var errAuthRejected = errors.New("credential rejected")

func newHTTPLink(name string, s Server, lookup func(string) (string, bool)) (*httpLink, error) {
	header, value := "", ""
	if s.Auth != nil {
		secret, ok := lookup(s.Auth.Env)
		if !ok || secret == "" {
			return nil, newError(CodeConfig, name, "", "auth needs %s, which is not set in this environment", s.Auth.Env)
		}
		value = secret
		header = "Authorization"
		if s.Auth.Type == "bearer" {
			value = "Bearer " + secret
		} else {
			header = s.Auth.Name
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	l := &httpLink{endpoint: s.URL, ctx: ctx, cancel: cancel}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	base.ForceAttemptHTTP2 = true
	l.client = &http.Client{
		Transport: &linkRoundTripper{link: l, base: base, header: header, value: value},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			l.redirected.Store(true)
			return errRedirect
		},
	}
	return l, nil
}

func (l *httpLink) transport() sdk.Transport {
	return &sdk.StreamableClientTransport{
		Endpoint:             l.endpoint,
		HTTPClient:           l.client,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
		MaxEventSize:         MaxFrameBytes,
	}
}

func (l *httpLink) kill()  { l.cancel() }
func (l *httpLink) close() { l.cancel(); l.client.CloseIdleConnections() }

func (l *httpLink) facts() linkFacts {
	return linkFacts{
		httpStatus: int(l.status.Load()),
		redirected: l.redirected.Load(),
		bodyTooBig: l.bodyTooBig.Load(),
		sendFailed: l.sendFailed.Load(),
		tlsFailed:  l.tlsFailed.Load(),
		exitCode:   -1,
	}
}

// linkRoundTripper ties every request to the link's lifetime, adds the
// credential header, records the status for classification and caps the
// body. The SDK detaches some requests from the caller's context, so the
// link's own cancellation is what bounds them.
type linkRoundTripper struct {
	link   *httpLink
	base   http.RoundTripper
	header string
	value  string
}

func (rt *linkRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if status := rt.link.status.Load(); status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, errAuthRejected
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(rt.link.ctx, cancel)
	out := req.Clone(ctx)
	if rt.header != "" {
		out.Header.Set(rt.header, rt.value)
	}
	resp, err := rt.base.RoundTrip(out)
	if err != nil {
		stop()
		cancel()
		if rt.link.ctx.Err() == nil && req.Context().Err() == nil {
			rt.link.sendFailed.Store(true)
			var certErr *tls.CertificateVerificationError
			var recordErr tls.RecordHeaderError
			if errors.As(err, &certErr) || errors.As(err, &recordErr) {
				rt.link.tlsFailed.Store(true)
			}
		}
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode >= 300 {
		rt.link.status.Store(int64(resp.StatusCode))
	}
	resp.Body = &cappedBody{body: resp.Body, remaining: MaxHTTPBody, link: rt.link, done: func() { stop(); cancel() }}
	return resp, nil
}

type cappedBody struct {
	body      io.ReadCloser
	remaining int64
	link      *httpLink
	done      func()
	once      sync.Once
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		var probe [1]byte
		if n, err := b.body.Read(probe[:]); n == 0 && errors.Is(err, io.EOF) {
			return 0, io.EOF
		}
		b.link.bodyTooBig.Store(true)
		return 0, errBodyTooLarge
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.body.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error {
	err := b.body.Close()
	b.once.Do(b.done)
	return err
}
