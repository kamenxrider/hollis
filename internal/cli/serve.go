// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/kamenxrider/hollis/internal/server"
	"github.com/spf13/cobra"
)

func newServeCmd(_ *rootFlags, newRunner newRunnerFunc) *cobra.Command {
	var (
		addr  string
		token string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the local OpenAI-compatible HTTP endpoint",
		Long: `Serve the local OpenAI-compatible HTTP endpoint (plan §19).

Endpoints:
  GET  /health                liveness
  GET  /v1/models             OpenAI-shaped model list
  POST /v1/chat/completions   Chat Completions subset (stream=false only)
  POST /v1/responses          Responses subset (stream=false only)

Binds to the loopback interface by default. A non-loopback bind requires
--token; all /v1 requests then need "Authorization: Bearer <token>".
Streaming is not supported: the Shortcuts transport returns the complete
response in a single call, and faking it is forbidden (plan principle 6).`,
		Example: `  hollis serve
  hollis serve --addr 127.0.0.1:1976 --token mysecret`,
		RunE: func(cmd *cobra.Command, args []string) error {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return usageErr(fmt.Errorf("invalid --addr %q: must be host:port", addr))
			}
			loopback := host == "127.0.0.1" || host == "localhost" || host == "::1"
			if !loopback && token == "" {
				return usageErr(errors.New("binding to a non-loopback address requires --token (remote bind requires auth, plan §30)"))
			}

			r := newRunner()
			srv := server.New(r, token)
			// Resolve bridges once at startup (results/macos-26-compat.md
			// steps 1+2): the transport invokes resolved refs and /v1/models
			// lists only tiers whose bridges resolve. Fakes skip resolution.
			if resolved, err := resolveForRunner(cmd.Context(), newRunner); err != nil {
				return configErr(err)
			} else if resolved != nil {
				applyResolvedRefs(r, resolved)
				srv.Available = availabilityMap(resolved)
			}
			httpServer := &http.Server{
				Addr:              addr,
				Handler:           srv.Handler(),
				ReadHeaderTimeout: 5 * time.Second,
			}
			fmt.Fprintf(cmd.OutOrStdout(), "hollis serve listening on http://%s (GET /health)\n", addr)
			if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return transportErr(err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:1976", "Listen address (loopback by default; non-loopback requires --token)")
	cmd.Flags().StringVar(&token, "token", "", "Require Authorization: Bearer <token> on /v1 endpoints")
	return cmd
}
