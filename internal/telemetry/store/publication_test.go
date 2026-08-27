package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/fanout/internal/telemetry"
)

type publicationInspectLock struct {
	t                 *testing.T
	repository        *Repository
	id                string
	locked            bool
	unlockedBeforeHot bool
}

func (l *publicationInspectLock) Lock() {
	l.locked = true
	for _, signal := range []string{"spans", "logs", "metrics"} {
		final := filepath.Join(l.repository.Parquet.Dir(), signal, l.id+".parquet")
		if _, err := os.Stat(final); !os.IsNotExist(err) {
			l.t.Fatalf("%s became visible before publication lock: %v", signal, err)
		}
		if _, err := os.Stat(final + ".pending"); err != nil {
			l.t.Fatalf("%s was not durably staged before publication lock: %v", signal, err)
		}
	}
}

func (l *publicationInspectLock) Unlock() {
	if rows := l.repository.Spans.RowCount(); rows != 0 {
		l.t.Fatalf("hot segment encoded while query publication gate was held: %d rows", rows)
	}
	l.unlockedBeforeHot = true
	l.locked = false
}

func TestCommitStagesOutsidePublicationLock(t *testing.T) {
	repository, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	const id = "atomic-batch"
	lock := &publicationInspectLock{t: t, repository: repository, id: id}
	repository.SetParquetPublishLock(lock)
	batch := Batch{
		ID:      id,
		Spans:   []telemetry.Span{{Namespace: "default", TraceID: "00000000000000000000000000000001", SpanID: "0000000000000001"}},
		Logs:    []telemetry.Log{{Namespace: "default", Body: "body"}},
		Metrics: []telemetry.Metric{{Namespace: "default", Name: "metric"}},
	}
	if err := repository.Commit(batch); err != nil {
		t.Fatal(err)
	}
	if lock.locked {
		t.Fatal("publication lock remained held after commit")
	}
	if !lock.unlockedBeforeHot {
		t.Fatal("publication lock was not released before hot-index encoding")
	}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if _, err := os.Stat(filepath.Join(repository.Parquet.Dir(), signal, id+".parquet")); err != nil {
			t.Fatalf("%s final file: %v", signal, err)
		}
	}
}
