// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.
package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/kamenxrider/hollis/internal/runner"
)

func (s *Server) stageNativeImages(ctx context.Context, w http.ResponseWriter, request preparedRequest) (stagedImages, bool) {
	if len(request.Images) != runner.NativeMaxImages {
		writeRequestValidationError(w, unsupportedParameter("local accepts exactly 1 image; the maximum is 1"))
		return stagedImages{}, false
	}
	stager := s.stageImages
	if stager == nil {
		stager = stageImages
	}
	staged, err := stager(ctx, request.Images)
	if err == nil {
		return staged, true
	}
	var validation *requestValidationError
	switch {
	case errors.As(err, &validation):
		writeRequestValidationError(w, err)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeAPIError(w, http.StatusGatewayTimeout, "server_error", "native_timeout", "the image request exceeded its deadline or was canceled")
	default:
		writeAPIError(w, http.StatusInternalServerError, "server_error", "image_staging_failed", "image input could not be prepared")
	}
	return stagedImages{}, false
}
