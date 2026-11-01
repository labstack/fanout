package engine

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func mockDuck(t *testing.T) (*Duck, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	return &Duck{DB: db}, mock
}

func shortContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

// (a) Sustained overload: every statement times out, but each returns at its
// deadline, so the engine honours deadlines and is alive.
func TestStatementsThatTimeOutAndReturnAreNoStall(t *testing.T) {
	d, mock := mockDuck(t)
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	for range 3 {
		mock.ExpectQuery("SELECT 1").WillDelayFor(time.Second).WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	}
	var one int
	if err := d.QueryRowScan(shortContext(t), []any{&one}, "SELECT 1"); err == nil {
		t.Fatal("a hung statement answered")
	}
	if rows, err := d.QueryContext(shortContext(t), "SELECT 1"); err == nil {
		for rows.Next() {
		}
		_ = rows.Close()
	}
	if d.Stalled() || d.inFlight() != 0 {
		t.Fatalf("stalled=%t in flight=%d after statements returned at their deadline", d.Stalled(), d.inFlight())
	}
	// Recently progressing, so liveness does not even ping.
	if err := d.Live(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// (b) A statement still running past its deadline plus the grace means the
// engine has stopped honouring deadlines.
func TestAStatementPastItsDeadlineIsAStall(t *testing.T) {
	d, _ := mockDuck(t)
	late, cancel := context.WithDeadline(t.Context(), time.Now().Add(-overrunGrace-time.Second))
	defer cancel()
	done := d.begin(late)
	if !d.Stalled() {
		t.Fatal("a statement overrunning its deadline is a stall")
	}
	if err := d.Live(t.Context()); !errors.Is(err, ErrStalled) {
		t.Fatalf("live = %v, want ErrStalled", err)
	}
	done(late.Err())
	if d.Stalled() {
		t.Fatal("a statement that returned is no stall")
	}
	within := d.begin(shortContext(t))
	defer within(nil)
	if d.Stalled() {
		t.Fatal("a statement within its deadline and grace is no stall")
	}
	if overrunGrace < maxQueryTimeoutMs*time.Millisecond {
		t.Fatalf("grace %s is shorter than the longest read", overrunGrace)
	}
}

// (c) An idle pool whose ping fails is dead.
func TestAnIdlePoolWhosePingFailsIsDead(t *testing.T) {
	d, mock := mockDuck(t)
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	mock.ExpectQuery("SELECT 1").WillReturnError(errors.New("database is closed"))
	if err := d.Live(t.Context()); err == nil {
		t.Fatal("an idle pool that cannot answer is alive")
	}
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	if err := d.Live(t.Context()); err != nil {
		t.Fatalf("an idle pool that answers is alive: %v", err)
	}
}

// (d) A saturated pool: the ping cannot get a connection, but every holder
// is within its deadline, so the engine is overloaded, not hung.
func TestASaturatedPoolIsAlive(t *testing.T) {
	d, _ := mockDuck(t)
	d.DB.SetMaxOpenConns(1)
	held, err := d.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := d.begin(ctx)
	defer done(nil)
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	if err := d.Live(shortContext(t)); err != nil {
		t.Fatalf("a saturated pool is alive: %v", err)
	}
}

// A broken engine fails every statement at once. Its errors show nothing
// runs, so they are no progress: liveness pings, and the failed ping is dead.
func TestEngineErrorsAreNoProgress(t *testing.T) {
	d, mock := mockDuck(t)
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	broken := errors.New("IO Error: could not read file")
	for range 2 {
		mock.ExpectQuery("SELECT 1").WillReturnError(broken)
	}
	var one int
	if err := d.QueryRowScan(shortContext(t), []any{&one}, "SELECT 1"); !errors.Is(err, broken) {
		t.Fatal(err)
	}
	if _, err := d.QueryContext(shortContext(t), "SELECT 1"); !errors.Is(err, broken) {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT 1").WillReturnError(broken)
	if err := d.Live(t.Context()); err == nil {
		t.Fatal("an engine failing every statement is alive")
	}
	// No row is an answer.
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}))
	if err := d.QueryRowScan(t.Context(), []any{&one}, "SELECT 1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err := d.Live(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// A read without a deadline has none to overrun, so it is not tracked, and
// rows a caller holds after the engine produced them are its own time.
func TestOnlyEngineTimeOnADeadlineIsTracked(t *testing.T) {
	d, mock := mockDuck(t)
	done := d.begin(t.Context())
	if d.inFlight() != 0 {
		t.Fatal("a read without a deadline is tracked")
	}
	done(nil)
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1).AddRow(2))
	rows, err := d.QueryContext(shortContext(t), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if d.inFlight() != 0 {
		t.Fatal("held rows are tracked as engine time")
	}
}

// A publisher holding readers out past its swap budget and the grace has
// stopped honouring its deadline too; reads then fail before the engine.
func TestAPublicationHeldPastItsBudgetIsAStall(t *testing.T) {
	d, _ := mockDuck(t)
	held := false
	if err := d.PublishParquet(t.Context(), func(context.Context) error { held = d.publishing.Load() != 0; return nil }); err != nil {
		t.Fatal(err)
	}
	if !held || d.publishing.Load() != 0 || d.Stalled() {
		t.Fatalf("held=%t publishing=%d stalled=%t", held, d.publishing.Load(), d.Stalled())
	}
	d.publishing.Store(time.Now().Add(-parquetSwapBudget - overrunGrace - time.Second).UnixNano())
	if !d.Stalled() {
		t.Fatal("a publication held past its budget and grace is a stall")
	}
}
