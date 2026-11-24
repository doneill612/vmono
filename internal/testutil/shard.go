package testutil

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	// shardsEnv sets how many processes a sharded package's tests are spread
	// over. 1 turns sharding off. Neither name starts with VMONO_, because vmono
	// passes variables that do on to the harnesses it launches.
	shardsEnv = "VMTEST_SHARDS"
	// childEnv marks a process as one batch of a sharded run.
	childEnv = "VMTEST_SHARD_CHILD"
	// maxShards caps the default process count: the tests mostly wait on git,
	// and more of them than this only compete for the disk.
	maxShards = 8
	// batchesPerShard is how many batches each worker runs on average. Several
	// small ones keep the workers evenly busy whichever tests are slow.
	batchesPerShard = 3
)

// Main runs a package's tests spread over several processes, and returns the
// exit code. Call it from TestMain as os.Exit(testutil.Main(m)).
//
// The tests here can't use t.Parallel: NewEnv isolates each one with
// environment variables and a working directory, which belong to the whole
// process, and others swap package variables. Separate processes keep all of
// that apart. The test binary lists its tests, splits them into batches, and
// runs itself once per batch with -test.run, a few at a time.
//
// The output of a batch is shown only when it fails, and every listed test
// must be seen to finish, so a test can't be skipped by accident. A run that
// asks for anything beyond plain `go test` (-run, -v, -json, coverage,
// profiles and so on) is not sharded, nor is one with VMTEST_SHARDS=1.
func Main(m *testing.M) int {
	flag.Parse()
	if os.Getenv(childEnv) != "" {
		os.Unsetenv(childEnv)
		return runHere(m)
	}
	n := shardCount(os.Getenv(shardsEnv), runtime.NumCPU())
	if n < 2 || !shardable(setFlags()) {
		return runHere(m)
	}
	names, err := listTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "listing tests to shard: %v\n", err)
		return 1
	}
	if len(names) < 2 {
		return runHere(m)
	}
	return runBatches(split(names, n*batchesPerShard), n, runBatch)
}

// shardCount is the number of processes to use, given the value of
// VMTEST_SHARDS and the number of CPUs.
func shardCount(env string, cpus int) int {
	if n, err := strconv.Atoi(env); err == nil && n > 0 {
		return n
	}
	return min(cpus, maxShards)
}

// setFlags returns the flags given on the command line, by name.
func setFlags() map[string]string {
	set := map[string]string{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = f.Value.String() })
	return set
}

// shardable reports whether a run with these flags means the same thing when
// split into batches. Only the flags known to are allowed.
func shardable(set map[string]string) bool {
	for name, value := range set {
		switch name {
		case "test.paniconexit0", "test.timeout", "test.testlogfile", "test.short":
		case "test.count":
			if value != "1" {
				return false
			}
		case "test.v":
			if value != "false" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// listTests asks this test binary for the names of everything -test.run
// selects from.
func listTests() ([]string, error) {
	cmd := exec.Command(os.Args[0], "-test.list", ".")
	cmd.Env = append(os.Environ(), childEnv+"=1")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Fields(string(out)) {
		if strings.HasPrefix(line, "Test") || strings.HasPrefix(line, "Example") || strings.HasPrefix(line, "Fuzz") {
			names = append(names, line)
		}
	}
	return names, nil
}

// split deals names into at most n non-empty batches, like cards, so
// neighbours in a file, which tend to be alike, land in different batches.
func split(names []string, n int) [][]string {
	n = min(n, len(names))
	batches := make([][]string, n)
	for i, name := range names {
		batches[i%n] = append(batches[i%n], name)
	}
	return batches
}

// runBatches runs every batch, at most workers at a time, and returns the exit
// code for the whole run. A failed batch doesn't stop the others.
func runBatches(batches [][]string, workers int, run func(names []string) (out string, err error)) int {
	queue := make(chan []string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	code := 0
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for names := range queue {
				out, err := run(names)
				missing := unfinished(names, out)
				if err == nil && len(missing) == 0 {
					continue
				}
				mu.Lock()
				code = 1
				fmt.Print(quiet(out))
				if err != nil {
					fmt.Printf("test batch: %v\n", err)
				}
				if len(missing) > 0 {
					fmt.Printf("test batch did not finish: %s\n", strings.Join(missing, ", "))
				}
				mu.Unlock()
			}
		}()
	}
	for _, b := range batches {
		queue <- b
	}
	close(queue)
	wg.Wait()
	return code
}

// runBatch runs the named tests in a process of their own. It is verbose so
// the result of each test can be read from the output. The test log, which
// `go test` uses to decide whether a result can be cached, is not passed on:
// the batches would overwrite each other's, and with none the result of a
// sharded run is never cached.
func runBatch(names []string) (string, error) {
	args := []string{"-test.v", "-test.run", "^(" + strings.Join(names, "|") + ")$"}
	for name, value := range setFlags() {
		switch name {
		case "test.paniconexit0", "test.timeout", "test.short":
			args = append(args, "-"+name+"="+value)
		}
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// resultLine matches a test's result in verbose output. It isn't anchored,
// since a test may print without a final newline.
var resultLine = regexp.MustCompile(`--- (?:PASS|FAIL|SKIP): (\S+) \(`)

// unfinished returns the tests whose result is not in out.
func unfinished(names []string, out string) []string {
	done := map[string]bool{}
	for _, m := range resultLine.FindAllStringSubmatch(out, -1) {
		done[m[1]] = true
	}
	var missing []string
	for _, n := range names {
		if !done[n] {
			missing = append(missing, n)
		}
	}
	return missing
}

// quiet drops the progress lines that verbose output adds, leaving what a
// plain run would show.
func quiet(out string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(out, "\n") {
		l := strings.TrimLeft(line, " ")
		if strings.HasPrefix(l, "=== ") || strings.HasPrefix(l, "--- PASS") || strings.TrimSpace(l) == "PASS" {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// runHere runs the tests in this process, with a place for NewSource to keep
// the repos it copies.
func runHere(m *testing.M) int {
	dir, err := os.MkdirTemp("", "vmono-test-templates-")
	if err == nil {
		templateRoot = dir
		defer os.RemoveAll(dir)
	}
	return m.Run()
}
