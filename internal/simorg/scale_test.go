package simorg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/recall"
	"github.com/kordloom/whodar/internal/resolve"
)

// scaleEnv opts a run into the scale measurements. They synthesize large
// companies and take minutes, so an ordinary `go test ./...` skips them.
const scaleEnv = "WHODAR_SCALE"

// TestScale measures what whodar costs as a company grows: how long ingest
// takes, how large the files get, how long a cold start takes, and how fast it
// answers. The store is one JSON file read whole on every command, so the
// question this answers is where that stops being reasonable.
//
//	WHODAR_SCALE=1 go test ./internal/simorg/ -run TestScale -v -timeout 30m
func TestScale(t *testing.T) {
	if os.Getenv(scaleEnv) == "" {
		t.Skipf("set %s=1 to measure how whodar scales", scaleEnv)
	}
	sizes := []struct {
		Name string
		Spec Spec
	}{
		{"team", Spec{People: 50, Channels: 20, Topics: 16, ThreadsPerChannel: 25, ChatterPerChannel: 200}},
		{"department", Spec{People: 250, Channels: 60, Topics: 16, ThreadsPerChannel: 60, ChatterPerChannel: 400}},
		{"company", Spec{People: 1000, Channels: 150, Topics: 16, ThreadsPerChannel: 100, ChatterPerChannel: 600}},
		{"enterprise", Spec{People: 5000, Channels: 400, Topics: 16, ThreadsPerChannel: 120, ChatterPerChannel: 800}},
	}
	t.Logf("%-11s %7s %7s %8s %9s %9s %9s %8s %8s %9s",
		"size", "people", "convos", "ingest", "index", "episodes", "cold", "ask", "recall", "heap")
	for _, size := range sizes {
		size.Spec.Seed = 11
		t.Run(size.Name, func(t *testing.T) {
			m := measure(t, size.Spec)
			t.Logf("%-11s %7d %7d %8s %9s %9s %9s %8s %8s %9s",
				size.Name, m.people, m.episodes, short(m.ingest), bytes(m.indexBytes),
				bytes(m.episodeBytes), short(m.coldStart), short(m.askLatency),
				short(m.recallLatency), bytes(int64(m.heap)))
		})
	}
}

// measurement is what one company size cost.
type measurement struct {
	// people and episodes are the size actually produced.
	people, episodes int
	// ingest is how long the connectors and indexer took.
	ingest time.Duration
	// indexBytes and episodeBytes are the files on disk.
	indexBytes, episodeBytes int64
	// coldStart is how long loading both files from disk took, which every
	// command pays before it can answer anything.
	coldStart time.Duration
	// askLatency and recallLatency are median-ish answer times.
	askLatency, recallLatency time.Duration
	// heap is bytes resident after loading, which is what a long-lived serve
	// or bot process holds.
	heap uint64
}

// measure builds a company of the given size and times the whole path.
func measure(t *testing.T, spec Spec) measurement {
	t.Helper()
	dir := t.TempDir()

	start := time.Now()
	built, err := Build(spec, dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := measurement{
		people:   len(built.Index.Graph.People),
		episodes: built.Episodes.Len(),
		ingest:   time.Since(start),
	}

	indexPath := filepath.Join(dir, "index.json")
	episodePath := filepath.Join(dir, "episodes.json")
	if err := built.Index.Save(indexPath); err != nil {
		t.Fatalf("save index: %v", err)
	}
	if err := built.Episodes.Save(episodePath); err != nil {
		t.Fatalf("save episodes: %v", err)
	}
	m.indexBytes = fileSize(t, indexPath)
	m.episodeBytes = fileSize(t, episodePath)

	// Cold start is what a person waits for before every single CLI answer.
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	start = time.Now()
	loadedIndex, err := index.Load(indexPath)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	loadedEpisodes, err := episode.Load(episodePath)
	if err != nil {
		t.Fatalf("load episodes: %v", err)
	}
	m.coldStart = time.Since(start)

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc {
		m.heap = after.HeapAlloc - before.HeapAlloc
	}

	m.askLatency = timeAsk(t, loadedIndex, built)
	m.recallLatency = timeRecall(t, loadedIndex, loadedEpisodes, built)
	return m
}

// timeAsk measures answering who-knows questions against a loaded index.
func timeAsk(t *testing.T, ix *index.Index, built *Built) time.Duration {
	t.Helper()
	res := resolve.NewKeyword(ix)
	var total time.Duration
	asked := 0
	for _, q := range built.Org.Questions {
		if q.Kind != KindWhoKnows {
			continue
		}
		start := time.Now()
		if _, err := res.Resolve(context.Background(), q.Text, 5); err != nil {
			t.Fatalf("ask: %v", err)
		}
		total += time.Since(start)
		asked++
	}
	return per(total, asked)
}

// timeRecall measures answering recall questions against loaded files.
func timeRecall(t *testing.T, ix *index.Index, store *episode.Store, built *Built) time.Duration {
	t.Helper()
	res := recall.New(store, ix)
	var total time.Duration
	asked := 0
	for _, q := range built.Org.Questions {
		if q.Kind != KindRecall || asked >= 200 {
			continue
		}
		start := time.Now()
		res.Resolve(context.Background(), recall.Query{Text: q.Text, Person: q.Asker, Limit: 5})
		total += time.Since(start)
		asked++
	}
	return per(total, asked)
}

// per averages a total over a count.
func per(total time.Duration, n int) time.Duration {
	if n == 0 {
		return 0
	}
	return total / time.Duration(n)
}

// fileSize returns a file's size on disk.
func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

// bytes renders a byte count the way a person reads one.
func bytes(n int64) string {
	switch {
	case n > 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n > 1<<20:
		return fmt.Sprintf("%.0fMB", float64(n)/(1<<20))
	case n > 1<<10:
		return fmt.Sprintf("%.0fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// short renders a duration without spurious precision.
func short(d time.Duration) string {
	switch {
	case d > time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d > time.Millisecond:
		return fmt.Sprintf("%.0fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	}
}
