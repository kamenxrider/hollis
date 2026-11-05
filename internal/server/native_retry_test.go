package server

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestNativeModelBusyIsServiceUnavailable(t *testing.T) {
	status, detail := nativeError(&runner.Error{
		Kind: runner.KindModelBusy, Err: errors.New("PRIVATE SDK DESCRIPTION"),
	})
	if status != http.StatusServiceUnavailable || detail["code"] != "model_busy" ||
		strings.Contains(detail["message"].(string), "PRIVATE") {
		t.Fatalf("status=%d detail=%v", status, detail)
	}
}
