package runner

import (
	"encoding/json"
	"os"
	"testing"
)

// Numeric regression fixture only: no remote content or network is needed.
func parallelBudget(t *testing.T) map[string]int {
	t.Helper()
	data, err := os.ReadFile("testdata/parallel-budget.json")
	if err != nil {
		t.Fatal(err)
	}
	var counts map[string]int
	if err := json.Unmarshal(data, &counts); err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestNativeBudgetVocabulary(t *testing.T) {
	if !validNativeBudget(parallelBudget(t)) {
		t.Fatal("numeric fixture rejected")
	}
	for _, mutation := range []func(map[string]int){
		func(c map[string]int) { delete(c, "tools_bytes") },
		func(c map[string]int) { c["private_content"] = 1 },
		func(c map[string]int) { c["tools_tokens"] = -1 },
		func(c map[string]int) { c["input_tokens"]++ },
		func(c map[string]int) { c["tools_bytes"] = 1<<30 + 1 },
	} {
		counts := parallelBudget(t)
		mutation(counts)
		if validNativeBudget(counts) {
			t.Fatal("unreviewed diagnostic accepted")
		}
	}
}

func TestNativeParallelBudgetRefusal(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			flag := ""
			if enabled {
				flag = "1"
			}
			t.Setenv("HOLLIS_DEBUG_BUDGET", flag)
			counts := parallelBudget(t)
			if counts["input_tokens"]+counts["answer_reserved_tokens"] <= counts["context_tokens"] {
				t.Fatal("fixture must exceed context capacity")
			}
			budget := nativePrint(nativeLine("budget", map[string]any{
				"request_id": "test-request", "budget": counts,
			}))
			r := fakeNative(t, "if [ \"$#\" -eq 0 ]; then cat >/dev/null\n"+
				nativePrint(metadataStatus(true))+"exit 0\nfi\ncat >/dev/null\n"+
				nativePrint(nativeRequestStatus(true))+budget+
				nativePrint(nativeLine("error", map[string]any{
					"request_id": "test-request", "kind": KindContextCapacity,
				})))
			_, err := r.RunToolRound(t.Context(), ModelLocal, metadataRound(t))
			want := KindNativeProtocol
			if enabled {
				want = KindContextCapacity
			}
			requireNativeKind(t, err, want)
		})
	}
}
