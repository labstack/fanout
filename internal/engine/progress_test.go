package engine

import (
	"context"
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

// Statements that hang until their deadline are no progress, even though
// connections are free and each cancelled statement returns: otherwise a
// wedged engine probed every few seconds would never count as stalled.
func TestTimedOutStatementsAreNoProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	d := &Duck{DB: db}
	stale := time.Now().Add(-stallLimit - time.Second).UnixNano()
	d.progress.Store(stale)
	hang := func() {
		mock.ExpectQuery("SELECT 1").WillDelayFor(time.Second).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	}
	hang()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	var one int
	if err := d.QueryRowScan(ctx, []any{&one}, "SELECT 1"); err == nil {
		t.Fatal("a hung statement answered")
	}
	hang()
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if rows, err := d.QueryContext(ctx, "SELECT 1"); err == nil {
		for rows.Next() {
		}
		_ = rows.Close()
	}
	if !d.Stalled() {
		t.Fatal("timed-out statements counted as progress")
	}
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	if err := d.QueryRowScan(t.Context(), []any{&one}, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if d.Stalled() {
		t.Fatal("a statement that completed is progress")
	}
	d.progress.Store(stale)
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1).AddRow(2))
	rows, err := d.QueryContext(t.Context(), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if err := rows.Close(); err != nil || d.Stalled() {
		t.Fatalf("rows read to the end are progress: err=%v stalled=%t", err, d.Stalled())
	}
}
