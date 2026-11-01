//go:build race

package runner

// raceEnabled marks race-detector builds, where allocator overhead makes
// RSS-proxy assertions meaningless. See run_bounds_test.go.
const raceEnabled = true
