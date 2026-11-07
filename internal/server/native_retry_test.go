package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestNativeUsageIsBadRequest(t *testing.T) {
	status, detail := nativeError(&runner.Error{Kind: runner.KindUsage, Err: errors.New("invalid native tool step")})
	if status != http.StatusBadRequest || detail["type"] != "invalid_request_error" || detail["code"] != "invalid_input" ||
		detail["message"] != "invalid native tool step" {
		t.Fatalf("status=%d detail=%v", status, detail)
	}
}

func TestNativeModelBusyIsServiceUnavailable(t *testing.T) {
	status, detail := nativeError(&runner.Error{
		Kind: runner.KindModelBusy, Err: errors.New("PRIVATE SDK DESCRIPTION"),
	})
	if status != http.StatusServiceUnavailable || detail["code"] != "model_busy" ||
		strings.Contains(detail["message"].(string), "PRIVATE") {
		t.Fatalf("status=%d detail=%v", status, detail)
	}
}
