package engine

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEmptyAffectedServiceSetAdvancesOnlyWatermark(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("FROM rollup_state").WithArgs(serviceRollupStateKey).WillReturnRows(sqlmock.NewRows([]string{"watermark"}).AddRow(100))
	mock.ExpectBegin()
	mock.ExpectExec("CREATE OR REPLACE TEMP TABLE service_pass_affected").WithArgs(int64(100), int64(200), int64(100), int64(200), int64(100), int64(200)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT epoch_ns").WillReturnRows(sqlmock.NewRows([]string{"lo", "hi"}).AddRow(nil, nil))
	mock.ExpectExec("DROP TABLE service_pass_affected").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO rollup_state").WithArgs(serviceRollupStateKey, int64(200)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	d := &Duck{DB: db, writeDB: db, publicationBounds: func() (int64, int64, int64) { return 200, 200, 100 }}
	if n, err := d.refreshServiceRollup(t.Context()); err != nil || n != 0 {
		t.Fatalf("empty pass rows=%d error=%v", n, err)
	}
	if d.rollupWatermarks[serviceRollupStateKey] != 200 {
		t.Fatal("empty pass did not advance watermark")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceBoundsRebuildSignedBoundaryMinutes(t *testing.T) {
	db := openTestDuck(t)
	if err := CreateTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateViews(db); err != nil {
		t.Fatal(err)
	}
	d := &Duck{DB: db, writeDB: db, publicationBounds: tablePublicationBounds(db)}
	base := time.Unix(0, 0).UTC().Add(-time.Minute)
	insert := func(id string, stamp time.Time, ingest int64) {
		insertRollupTestSpan(t, db, rollupTestSpan{namespace: "ns", traceID: id, spanID: id, service: "api", start: stamp, duration: time.Millisecond, ingested: ingest})
		// Bind the signed fixture as an integer through make_timestamp_ns;
		// the generic test insertion helper has no explicit parameter cast.
		if _, err := db.Exec(`UPDATE telemetry.spans SET start_time=make_timestamp_ns(?) WHERE span_id=?`, stamp.UnixNano(), id); err != nil {
			t.Fatal(err)
		}
	}
	insert("signed-first", base, 100)
	insert("signed-last", base.Add(time.Minute-time.Microsecond), 100)
	insert("upper-edge", base.Add(time.Minute), 100)
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireServiceRollupSpans(t, db, "ns", base, "api", 2)
	requireServiceRollupSpans(t, db, "ns", base.Add(time.Minute), "api", 1)
	insert("late", base.Add(time.Nanosecond), 200)
	if _, err := d.refreshServiceRollup(t.Context()); err != nil {
		t.Fatal(err)
	}
	requireServiceRollupSpans(t, db, "ns", base, "api", 3)
	requireServiceRollupSpans(t, db, "ns", base.Add(time.Minute), "api", 1)
}
