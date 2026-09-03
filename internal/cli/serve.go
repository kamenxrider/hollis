// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kamenxrider/hollis/internal/server"
	"github.com/spf13/cobra"
)

func newServeCmd(_ *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var (
		addr           string
		tokenFile      string
		allowRemote    bool
		maxConcurrency int
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the local OpenAI-compatible HTTP endpoint",
		Long: `Serve Hollis's completed-text OpenAI-compatible subset.

Endpoints:
  GET  /health
  GET  /v1/models
  POST /v1/chat/completions
  POST /v1/responses

Loopback is the default. A non-loopback bind requires both --allow-remote and
authentication supplied by --token-file or HOLLIS_API_TOKEN. Hollis does not
provide TLS: expose it only through an encrypted trusted path such as Tailscale,
WireGuard, or an SSH tunnel. Streaming is intentionally unsupported.`,
		Example: `  hollis serve
  hollis serve --addr 127.0.0.1:1978 --token-file /private/path/hollis.token
  HOLLIS_API_TOKEN='<at least 32 bytes>' hollis serve --allow-remote --addr 100.64.0.2:1978`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := noExtraArgs("serve")(cmd, args); err != nil {
				return err
			}
			if maxConcurrency < 1 || maxConcurrency > 4 {
				return usageErr(fmt.Errorf("invalid --max-concurrency %d: choose 1 through 4", maxConcurrency))
			}
			if _, _, err := net.SplitHostPort(addr); err != nil {
				return usageErr(fmt.Errorf("invalid --addr %q: must be host:port", addr))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxConcurrency < 1 || maxConcurrency > 4 {
				return usageErr(fmt.Errorf("invalid --max-concurrency %d: choose 1 through 4", maxConcurrency))
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return usageErr(fmt.Errorf("invalid --addr %q: must be host:port", addr))
			}
			tokenSpecified := tokenFile != ""
			if _, present := os.LookupEnv("HOLLIS_API_TOKEN"); present {
				tokenSpecified = true
			}
			token, err := loadServeToken(tokenFile)
			if err != nil {
				return configErr(err)
			}
			if tokenSpecified && len([]byte(token)) < 32 {
				return usageErr(errors.New("API token must be at least 32 bytes"))
			}
			remoteAuthorized := allowRemote && token != ""
			loopback, literalIP := literalLoopback(host)
			if literalIP && !loopback && !remoteAuthorized {
				return usageErr(errors.New("non-loopback binding requires both --allow-remote and authentication"))
			}

			// Hostnames cannot be trusted by name: even "localhost" can be
			// remapped. Bind without serving, then authorize the actual address.
			var listener net.Listener
			if !literalIP {
				listener, err = net.Listen("tcp", addr)
				if err != nil {
					return transportErr(fmt.Errorf("listen on %s: %w", addr, err))
				}
				defer listener.Close()
				if !listenerLoopback(listener.Addr()) && !remoteAuthorized {
					return usageErr(errors.New("non-loopback binding requires both --allow-remote and authentication"))
				}
			}

			r := newRunner()
			api := server.New(r, token)
			api.MaxConcurrency = maxConcurrency
			if resolved, resolveErr := resolveForRunner(cmd.Context(), newRunner); resolveErr != nil && !canAttemptAfterDiscoveryFailure(resolved, "auto") {
				return resolutionCLIError(resolveErr)
			} else if resolved != nil {
				applyResolvedRefs(r, resolved)
				api.Available = availabilityMap(resolved)
			}

			httpServer := &http.Server{
				Handler:           api.Handler(),
				ReadHeaderTimeout: 5 * time.Second,
				ReadTimeout:       15 * time.Second,
				WriteTimeout:      125 * time.Second,
				IdleTimeout:       60 * time.Second,
				MaxHeaderBytes:    1 << 20,
			}
			if listener == nil {
				listener, err = net.Listen("tcp", addr)
				if err != nil {
					return transportErr(fmt.Errorf("listen on %s: %w", addr, err))
				}
				defer listener.Close()
			}
			fmt.Fprintf(cmd.OutOrStdout(), "hollis serve listening on http://%s (GET /health)\n", listener.Addr())

			serveResult := make(chan error, 1)
			go func() { serveResult <- httpServer.Serve(listener) }()
			stopCtx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			select {
			case serveErr := <-serveResult:
				if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					return transportErr(serveErr)
				}
				return nil
			case <-stopCtx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := httpServer.Shutdown(shutdownCtx); err != nil {
					return transportErr(fmt.Errorf("graceful shutdown: %w", err))
				}
				serveErr := <-serveResult
				if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					return transportErr(serveErr)
				}
				return nil
			}
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:1978", "Listen address")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "Read the bearer token from a private regular file")
	cmd.Flags().BoolVar(&allowRemote, "allow-remote", false, "Allow a non-loopback bind when authentication is configured")
	cmd.Flags().IntVar(&maxConcurrency, "max-concurrency", 1, "Maximum simultaneous model runs (1-4)")
	return cmd
}

func literalLoopback(host string) (loopback bool, literalIP bool) {
	ip := net.ParseIP(host)
	if ip == nil {
		return false, false
	}
	return ip.IsLoopback(), true
}

func listenerLoopback(addr net.Addr) bool {
	tcpAddr, ok := addr.(*net.TCPAddr)
	return ok && tcpAddr.IP != nil && tcpAddr.IP.IsLoopback()
}

func loadServeToken(tokenFile string) (string, error) {
	environmentToken, envSet := os.LookupEnv("HOLLIS_API_TOKEN")
	if tokenFile != "" && envSet {
		return "", errors.New("set only one of --token-file or HOLLIS_API_TOKEN")
	}
	if tokenFile == "" {
		return trimOneLineEnding(environmentToken), nil
	}
	file, err := os.OpenFile(tokenFile, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect token file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("token file must be a regular file, not a symlink or special file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("token file must not be readable or writable by group or others")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	return trimOneLineEnding(string(data)), nil
}

func trimOneLineEnding(value string) string {
	value = strings.TrimSuffix(value, "\n")
	return strings.TrimSuffix(value, "\r")
}
