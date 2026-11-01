package engine

import (
	"context"
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
	done := d.begin(time.Now().Add(-overrunGrace - time.Second))
	if !d.Stalled() {
		t.Fatal("a statement overrunning its deadline is a stall")
	}
	if err := d.Live(t.Context()); !errors.Is(err, ErrStalled) {
		t.Fatalf("live = %v, want ErrStalled", err)
	}
	done()
	if d.Stalled() {
		t.Fatal("a statement that returned is no stall")
	}
	within := d.begin(time.Now())
	defer within()
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
	done := d.begin(time.Now().Add(time.Second))
	defer done()
	d.progress.Store(time.Now().Add(-idleLimit - time.Second).UnixNano())
	if err := d.Live(shortContext(t)); err != nil {
		t.Fatalf("a saturated pool is alive: %v", err)
	}
}
