package testutil

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSplitCoversEveryTestOnce(t *testing.T) {
	names := []string{"TestA", "TestB", "TestC", "TestD", "TestE"}
	for n := 1; n <= 8; n++ {
		var got []string
		for _, b := range split(names, n) {
			if len(b) == 0 {
				t.Errorf("n=%d: empty batch", n)
			}
			got = append(got, b...)
		}
		slices.Sort(got)
		if !slices.Equal(got, names) {
			t.Errorf("n=%d: batches hold %v", n, got)
		}
	}
}

func TestShardableOnlyForPlainRuns(t *testing.T) {
	plain := map[string]string{"test.paniconexit0": "true", "test.timeout": "20m0s", "test.count": "1", "test.testlogfile": "x", "test.v": "false"}
	if !shardable(plain) {
		t.Error("a plain run should be sharded")
	}
	for name, value := range map[string]string{
		"test.run": "TestX", "test.skip": "TestX", "test.list": ".", "test.v": "true", "test.bench": ".",
		"test.count": "2", "test.shuffle": "on", "test.coverprofile": "c.out", "test.failfast": "true",
	} {
		if shardable(map[string]string{name: value}) {
			t.Errorf("-%s=%s should not be sharded", name, value)
		}
	}
}

func TestShardCount(t *testing.T) {
	for _, c := range []struct {
		env        string
		cpus, want int
	}{{"", 4, 4}, {"", 32, maxShards}, {"1", 4, 1}, {"12", 4, 12}, {"junk", 2, 2}, {"0", 2, 2}} {
		if got := shardCount(c.env, c.cpus); got != c.want {
			t.Errorf("shardCount(%q, %d) = %d, want %d", c.env, c.cpus, got, c.want)
		}
	}
}

// TestRunBatchesReportsTrouble covers a batch that fails, one that crashes
// before all its tests ran, and one that passes while leaving a test out.
func TestRunBatchesReportsTrouble(t *testing.T) {
	pass := func(names []string) string {
		var b strings.Builder
		for _, n := range names {
			b.WriteString("=== RUN   " + n + "\n--- PASS: " + n + " (0.00s)\n")
		}
		return b.String()
	}
	batches := [][]string{{"TestA", "TestB"}, {"TestC"}}
	for name, c := range map[string]struct {
		run  func([]string) (string, error)
		want int
	}{
		"pass": {func(n []string) (string, error) { return pass(n), nil }, 0},
		"fail": {func(n []string) (string, error) {
			return strings.ReplaceAll(pass(n), "PASS: TestC", "FAIL: TestC"), errors.New("exit status 1")
		}, 1},
		"crash":   {func([]string) (string, error) { return "panic: boom\n", errors.New("exit status 2") }, 1},
		"skipped": {func(n []string) (string, error) { return pass(n[:len(n)-1]), nil }, 1},
	} {
		if got := runBatches(batches, 2, c.run); got != c.want {
			t.Errorf("%s: exit code %d, want %d", name, got, c.want)
		}
	}
	sub := "--- PASS: TestA/TestB (0.00s)\n"
	if got := unfinished([]string{"TestB"}, sub); len(got) != 1 {
		t.Error("a subtest's result was taken for a test's")
	}
}

func TestQuietKeepsFailures(t *testing.T) {
	out := "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n    b_test.go:3: bad\n--- FAIL: TestB (0.00s)\nFAIL\n"
	if got, want := quiet(out), "    b_test.go:3: bad\n--- FAIL: TestB (0.00s)\nFAIL\n"; got != want {
		t.Errorf("quiet = %q, want %q", got, want)
	}
}
