package cli

import (
	"testing"

	"github.com/kamenxrider/hollis/internal/runner"
)

func TestDoctorPCCQuotaIsInformational(t *testing.T) {
	stubConfigPath(t)
	stubResolution(t, []string{"AFM Bridge - Cloud", "AFM Bridge - Cloud Pro"}, true, 27)
	for _, route := range []string{"cloud", "cloud-pro"} {
		for _, status := range []string{"available", "approaching", "limit_reached", "unknown"} {
			r := &setupStatusRunner{status: runner.LocalStatus{Capabilities: &runner.HelperCapabilities{
				PCCQuota: &runner.PCCQuota{Status: status, ResetDate: "2026-01-02T03:04:05Z"},
			}}}
			got, err := runSetupFixture(t, r, "doctor", "--model", route, "--json")
			if err != nil || got["status"] != "ready" || got["inference_tested"] != false || got["pcc_quota"].(map[string]any)["status"] != status {
				t.Fatalf("route=%s quota=%s got=%v err=%v", route, status, got, err)
			}
		}
	}
}
