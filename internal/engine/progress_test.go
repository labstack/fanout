package engine

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// A read pool that hands out no connection and finishes no statement for
// longer than the longest reads may run is wedged; one that was merely idle
// answers a ping and is not.
func TestReadPoolStallsOnlyWithoutProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := &Duck{DB: db}
	if d.Stalled() {
		t.Fatal("a pool with no recorded progress is not stalled")
	}
	d.progress.Store(time.Now().Add(-stallLimit - time.Second).UnixNano())
	if !d.Stalled() {
		t.Fatal("no progress past the limit is a stall")
	}
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	if err := d.Ping(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d.Stalled() {
		t.Fatal("a ping that answers is progress")
	}
	if stallLimit < 3*maxQueryTimeoutMs*time.Millisecond {
		t.Fatalf("stall limit %s is shorter than three of the longest reads", stallLimit)
	}
}
