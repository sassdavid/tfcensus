package tfstate_test

import (
	"fmt"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// countingReader counts the bytes the parser reads.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err //nolint:wrapcheck // a pass-through reader
}

// parseLarge parses and indexes a 50 MB state streamed through a pipe, and returns the bytes
// read and the memory the Go runtime got from the OS by the end.
func parseLarge(t testing.TB) (int64, uint64, int) {
	t.Helper()
	pr, pw := io.Pipe()
	go writeLarge(pw)
	cr := &countingReader{r: pr}
	s, err := tfstate.Parse(cr)
	require.NoError(t, err)
	ix := tfstate.BuildIndex([]tfstate.UnitState{{
		Repo: "big", Path: "unit", Workspace: "default",
		Status: tfstate.StatusOK, State: s,
	}})
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return cr.n, ms.Sys, len(ix.Entries)
}

// TestLargeState is part of state-reader criterion 1: a 50 MB state is indexed with under
// 200 MB of memory. It doesn't run in parallel, so other tests' allocations don't count. Sys is
// all the memory the Go runtime has mapped, which bounds the process's resident set.
func TestLargeState(t *testing.T) { //nolint:paralleltest // measures the process's memory
	read, sys, entries := parseLarge(t)
	assert.GreaterOrEqual(t, read, int64(largeSize))
	assert.Greater(t, entries, 20000)
	assert.Less(t, sys, uint64(maxMemory), "runtime memory after indexing %d MB: %d MB", read>>20, sys>>20)
	t.Logf("indexed %d MB, %d instances, runtime memory %d MB", read>>20, entries, sys>>20)
}

func BenchmarkLargeState(b *testing.B) {
	for b.Loop() {
		read, sys, _ := parseLarge(b)
		b.SetBytes(read)
		b.ReportMetric(float64(sys>>20), "MB-sys")
	}
}

// fleet500 puts 500 states of about 5 KB into a fake S3 and returns their units.
func fleet500(t testing.TB) (*awstest.Fake, []discovery.Unit) {
	t.Helper()
	f := awstest.NewFake(t, lastModified())
	app := readFixture(t, "tf112-app.tfstate")
	units := make([]discovery.Unit, 500)
	for i := range units {
		path := fmt.Sprintf("live/unit-%03d", i)
		f.Put("acme-tfstate-111111111111", path+"/terraform.tfstate", app)
		units[i] = discovery.Unit{
			Repo: "fleet", Path: path, Kind: discovery.KindTerragrunt,
			Backend: &discovery.Backend{
				Type: discovery.BackendS3, Bucket: "acme-tfstate-111111111111",
				Key: path + "/terraform.tfstate", Region: "eu-west-1",
			},
			Target: discovery.Target{Account: "111111111111", Region: "eu-west-1"},
		}
	}
	return f, units
}

func index500(t testing.TB, f *awstest.Fake, units []discovery.Unit) *tfstate.Index {
	t.Helper()
	r := tfstate.NewReader(tfstate.Options{Client: fakeClients(f), Concurrency: 16})
	states := r.Read(t.Context(), units)
	for _, s := range states {
		require.Equal(t, tfstate.StatusOK, s.Status, s.Error)
	}
	return tfstate.BuildIndex(states)
}

// TestSpec_statereader_4: 500 states over a stubbed S3 at 16 concurrency complete in under 5 s.
func TestSpec_statereader_4(t *testing.T) {
	t.Parallel()

	f, units := fleet500(t)
	start := time.Now()
	ix := index500(t, f, units)
	elapsed := time.Since(start)
	assert.Len(t, ix.Units, 500)
	assert.Len(t, ix.Entries, 500*7)
	assert.Less(t, elapsed, 5*time.Second)
	t.Logf("500 states in %v", elapsed)
}

func BenchmarkIndex500States(b *testing.B) {
	f, units := fleet500(b)
	for b.Loop() {
		index500(b, f, units)
	}
}
