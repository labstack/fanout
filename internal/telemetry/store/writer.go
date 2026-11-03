package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/telemetry"
)

const (
	commitQueueDepth = maxCommitWorkers
	commitRetryLimit = 5
	// Bound group admission independently of encode time. Ten milliseconds
	// coalesces concurrent exports without imposing a 20ms floor on every
	// durable acknowledgement when the row target is not reached.
	groupAdmissionWindow    = 10 * time.Millisecond
	pressureAdmissionWindow = time.Second
	pressureAdmissionIdle   = 20 * time.Millisecond
	maxAdmissionRequests    = 512
	maxGroupBatchRows       = 50_000
	pressureGroupBatchRows  = 200_000
	maxCommitWorkers        = 4
	submissionQueueDepth    = 256
	writerShutdownGrace     = 30 * time.Second
)

type batchCommitter interface {
	Commit(context.Context, Batch) error
}

type compactionPressure interface {
	CompactionDebt() int
}

type Writer struct {
	admission      *ingestAdmission
	inFlightBudget int
	inFlightBytes  atomic.Int64
	repository     batchCommitter
	compaction     compactionPressure
	batchSize      int
	retryDelay     func(int) time.Duration
	groupWindow    time.Duration
	shutdownGrace  time.Duration
	done           chan struct{}
	stopping       chan struct{}
	submissionMu   sync.RWMutex
	submissions    chan *submission
}

// Submission states. Exactly one transition out of queued decides who releases
// the reservation: the commit that claims the request, or whoever abandons it.
const (
	queued int32 = iota
	claimed
	abandoned
)

type submission struct {
	// ctx is the sender's request. Once it is done before a commit claims the
	// submission, the sender has stopped waiting and will resend, so the
	// submission is abandoned unwritten.
	ctx   context.Context
	batch Batch
	// rows and bytes are measured once at submission: the byte estimate walks
	// every attribute map, and admission, grouping and release all need the
	// same figures. Grouping reads only these, never batch, which an
	// abandoned submission drops.
	rows  int
	bytes int
	state atomic.Int32
	ack   chan error
}

func newSubmission(ctx context.Context, batch Batch) *submission {
	return &submission{ctx: ctx, batch: batch, rows: batchRows(batch), bytes: batchBytes(batch), ack: make(chan error, 1)}
}

// commitJob is one atomic batch: its requests are merged only when a worker
// claims them, after skipping those whose senders have given up.
type commitJob struct {
	requests []*submission
}

// abandon drops a submission no commit has claimed. It releases the
// reservation and the rows at once, so the sender's retry is not shed for
// bytes held by the copy it replaces, and reports whether it won.
func (w *Writer) abandon(request *submission) bool {
	if !request.state.CompareAndSwap(queued, abandoned) {
		return false
	}
	request.batch = Batch{}
	w.release(request.bytes)
	metrics.RecordIngestAbandoned()
	return true
}

// fail answers every request no one else has settled with err.
func (w *Writer) fail(requests []*submission, err error) {
	for _, request := range requests {
		if request.state.CompareAndSwap(queued, claimed) {
			w.release(request.bytes)
			request.ack <- err
		}
	}
}

// ErrIngestOverBudget is returned when accepting a request would take the
// process past the bytes it is willing to hold in flight. Callers map it to a
// retryable status so a sender backs off and resends: shedding load is the
// point, and a dropped request would defeat it.
var ErrIngestOverBudget = errors.New("telemetry ingest is over its in-flight byte budget")

// reserve charges a batch's measured bytes against the in-flight budget,
// or refuses it.
//
// An empty ledger admits anything, however large. One request is one atomic
// batch and refusing the only thing in flight would shed load the process is
// not actually short of -- the budget exists to stop requests accumulating,
// not to reject a single large one. A budget of zero is unbounded, so an
// unconfigured deployment behaves exactly as it did before.
func (w *Writer) reserve(bytes int) error {
	if w.inFlightBudget <= 0 {
		return nil
	}
	cost := int64(bytes)
	for {
		current := w.inFlightBytes.Load()
		if current > 0 && current > int64(w.inFlightBudget)-cost {
			metrics.RecordIngestShed()
			return ErrIngestOverBudget
		}
		if w.inFlightBytes.CompareAndSwap(current, current+cost) {
			return nil
		}
	}
}

func (w *Writer) release(bytes int) {
	if w.inFlightBudget <= 0 {
		return
	}
	w.inFlightBytes.Add(-int64(bytes))
}

func NewWriter(repository *Repository, batchSize int) *Writer {
	w := &Writer{
		repository: repository, batchSize: batchSize, groupWindow: groupAdmissionWindow,
		done: make(chan struct{}), stopping: make(chan struct{}), submissions: make(chan *submission, submissionQueueDepth),
	}
	if repository != nil {
		w.SetAdmission(repository.CompactionDebt, nil)
	}
	return w
}

// SetInFlightBudget caps the decoded telemetry the writer will hold across
// concurrent requests. Zero is unbounded.
func (w *Writer) SetInFlightBudget(bytes int) { w.inFlightBudget = bytes }

func (w *Writer) Wait() { <-w.done }

// Submit returns after every row in the request belongs to a durably published
// atomic Parquet batch directory. A request whose ctx is done before its commit
// starts is dropped unwritten, so the sender's retry is the only copy. Once the
// commit has started it completes, so a sender that gives up during it and
// retries stores the rows twice: delivery is at least once in that window,
// because OTLP has no idempotency key that survives across requests.
func (w *Writer) Submit(ctx context.Context, batch Batch) error {
	if batchRows(batch) == 0 {
		return nil
	}
	// The caller owns the reservation until enqueue succeeds. The queue and
	// then the commit job retain it until a commit claims the request or the
	// caller abandons it.
	if !consumeAdmission(ctx, w, batchRows(batch)) {
		if err := w.Admit(ctx, batchRows(batch)); err != nil {
			return err
		}
	}
	request := newSubmission(ctx, batch)
	if err := w.reserve(request.bytes); err != nil {
		return err
	}
	if err := w.enqueueSubmission(ctx, request); err != nil {
		w.release(request.bytes)
		return err
	}
	select {
	case err := <-request.ack:
		return err
	case <-w.done:
		return errors.New("telemetry writer stopped before durable acknowledgement")
	case <-ctx.Done():
		w.abandon(request)
		return ctx.Err()
	}
}

func (w *Writer) enqueueSubmission(ctx context.Context, request *submission) error {
	w.submissionMu.RLock()
	defer w.submissionMu.RUnlock()
	// Once shutdown starts, never enqueue into an already drained queue.
	select {
	case <-w.stopping:
		return errors.New("telemetry writer is stopped")
	default:
	}
	select {
	case w.submissions <- request:
		metrics.UpdateQueueDepth("batch", len(w.submissions))
		return nil
	case <-w.stopping:
		return errors.New("telemetry writer is stopped")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) Run(ctx context.Context) error {
	defer func() {
		close(w.stopping)
		// Wake blocked enqueues before excluding producers and draining. A send
		// racing the stop can succeed, but must finish before this final drain.
		w.submissionMu.Lock()
		defer w.submissionMu.Unlock()
		for {
			select {
			case request := <-w.submissions:
				w.fail([]*submission{request}, errors.New("telemetry writer stopped before durable acknowledgement"))
			default:
				metrics.UpdateQueueDepth("batch", 0)
				close(w.done)
				return
			}
		}
	}()
	w.compaction, _ = w.repository.(compactionPressure)
	if w.admission != nil {
		ticker := time.NewTicker(admissionInterval)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ticker.C:
					w.admission.update()
				case <-w.done:
					return
				}
			}
		}()
	}
	jobs := make(chan commitJob, commitQueueDepth)
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	defer cancelWorkers()
	var workers sync.WaitGroup
	for range min(maxCommitWorkers, max(1, runtime.GOMAXPROCS(0))) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			w.commitWorker(workerCtx, jobs)
		}()
	}

	finish := func(graceful bool) error {
		close(jobs)
		if !graceful {
			cancelWorkers()
		}
		finished := make(chan struct{})
		go func() { workers.Wait(); close(finished) }()
		grace := w.shutdownGrace
		if grace <= 0 {
			grace = writerShutdownGrace
		}
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-finished:
		case <-timer.C:
			cancelWorkers()
			<-finished
		}
		return nil
	}

	for {

		select {
		case request := <-w.submissions:
			metrics.UpdateQueueDepth("batch", len(w.submissions))
			if err := w.enqueueSubmissions(ctx, request, jobs); err != nil {
				return errors.Join(err, finish(false))
			}
		case <-ctx.Done():
			return finish(true)
		}
	}
}

func (w *Writer) enqueueSubmissions(ctx context.Context, request *submission, out chan<- commitJob) (err error) {
	requests := make([]*submission, 0, maxAdmissionRequests)
	defer func() {
		if err != nil {
			w.fail(requests, err)
		}
	}()
	// Abandoned submissions already released everything; they take no place
	// in a group.
	rows, bytes := 0, 0
	admit := func(next *submission) {
		if next.ctx.Err() != nil && w.abandon(next) || next.state.Load() == abandoned {
			return
		}
		requests = append(requests, next)
		rows += next.rows
		bytes += next.bytes
	}
	admit(request)
	pressure := w.compaction != nil && w.compaction.CompactionDebt() >= admissionBandStart
	limit := w.batchLimit(pressure)
	if rows < limit && bytes < maxGroupBatchBytes {
		window := w.groupWindow
		if window <= 0 {
			window = groupAdmissionWindow
		}
		// Coalesce more exports before the hard debt stop is needed. Read
		// reclaimable debt once per group; low-load acknowledgements keep
		// their short window as soon as compaction relieves the pressure.
		var idle <-chan time.Time
		var idleTimer *time.Timer
		defer func() {
			if idleTimer != nil {
				idleTimer.Stop()
			}
		}()
		armIdle := func() {
			// Retry gaps must not produce tiny files. Once a normal-size batch is
			// available, release idle closed-loop senders so they can send again.
			if pressure && rows >= maxGroupBatchRows {
				if idleTimer == nil {
					idleTimer = time.NewTimer(pressureAdmissionIdle)
					idle = idleTimer.C
				} else {
					idleTimer.Reset(pressureAdmissionIdle)
				}
			}
		}
		if pressure {
			window = pressureAdmissionWindow
			armIdle()
		}
		timer := time.NewTimer(window)
		defer timer.Stop()
	admission:
		for len(requests) < maxAdmissionRequests && rows < limit && bytes < maxGroupBatchBytes {
			select {
			case next := <-w.submissions:
				admit(next)
				armIdle()
			case <-idle:
				break admission
			case <-timer.C:
				break admission
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	for len(requests) < maxAdmissionRequests {
		select {
		case next := <-w.submissions:
			admit(next)
		default:
			goto drained
		}
	}

drained:
	metrics.UpdateQueueDepth("batch", len(w.submissions))
	for len(requests) > 0 {
		firstRows := requests[0].rows
		// A full batch, a lone request, or a request that cannot share the
		// next batch needs no row copy through the group-commit buffer. One
		// oversized request remains one atomic directory; the limit is a
		// group-commit target, not a durability boundary.
		if firstRows >= limit || len(requests) == 1 || firstRows+requests[1].rows > limit {
			direct := requests[0]
			requests = requests[1:]
			if err := w.enqueueJob(ctx, out, commitJob{requests: []*submission{direct}}); err != nil {
				return err
			}
			continue
		}

		group := make([]*submission, 0, len(requests))
		rows, groupBytes := 0, 0
		for len(requests) > 0 {
			next := requests[0]
			nextRows := next.rows
			if len(group) > 0 && rows+nextRows > limit {
				break
			}
			if !groupBatchFits(groupBytes, next.bytes, len(group)) {
				break
			}
			requests = requests[1:]
			group = append(group, next)
			rows += nextRows
			groupBytes += next.bytes
			if rows >= limit {
				break
			}
		}
		if err := w.enqueueJob(ctx, out, commitJob{requests: group}); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) enqueueJob(ctx context.Context, out chan<- commitJob, job commitJob) error {
	select {
	case out <- job:
		return nil
	case <-ctx.Done():
		w.fail(job.requests, ctx.Err())
		return ctx.Err()
	}
}

func (w *Writer) commitWorker(ctx context.Context, jobs <-chan commitJob) {
	for job := range jobs {
		if err := ctx.Err(); err != nil {
			w.fail(job.requests, err)
			continue
		}
		job = w.claim(job)
		if len(job.requests) == 0 {
			continue
		}
		batch := job.batch()
		if err := w.commitBatch(ctx, batch); err != nil {
			w.finish(job, err)
			slog.Error("telemetry batch commit exhausted retries", "error", err)
			continue
		}
		metrics.RecordIngest("spans", len(batch.Spans))
		metrics.RecordIngest("logs", len(batch.Logs))
		metrics.RecordIngest("metrics", len(batch.Metrics))
		w.finish(job, nil)
	}
}

// claim takes the requests whose senders are still waiting as this commit
// starts, abandoning the rest. Their senders resend, so committing them as
// well would store their rows twice. The rest of a group still commits.
func (w *Writer) claim(job commitJob) commitJob {
	live := job.requests[:0]
	for _, request := range job.requests {
		if request.ctx.Err() != nil {
			w.abandon(request)
			continue
		}
		if request.state.CompareAndSwap(queued, claimed) {
			live = append(live, request)
		}
	}
	job.requests = live
	return job
}

// batch merges the job's requests into one atomic batch with a fresh ID,
// copying each row once. A lone request commits without copying its rows.
func (job commitJob) batch() Batch {
	if len(job.requests) == 1 {
		batch := job.requests[0].batch
		batch.ID = uuid.NewString()
		return batch
	}
	var spans, logs, metrics int
	for _, request := range job.requests {
		spans += len(request.batch.Spans)
		logs += len(request.batch.Logs)
		metrics += len(request.batch.Metrics)
	}
	batch := Batch{ID: uuid.NewString(), Spans: make([]telemetry.Span, 0, spans),
		Logs: make([]telemetry.Log, 0, logs), Metrics: make([]telemetry.Metric, 0, metrics)}
	for _, request := range job.requests {
		batch.Spans = append(batch.Spans, request.batch.Spans...)
		batch.Logs = append(batch.Logs, request.batch.Logs...)
		batch.Metrics = append(batch.Metrics, request.batch.Metrics...)
	}
	return batch
}

// finish settles claimed requests.
func (w *Writer) finish(job commitJob, err error) {
	for _, request := range job.requests {
		w.release(request.bytes)
		request.ack <- err
	}
}

// commitBatch retries the same batch under the same ID. Requests abandoned
// between attempts stay in it: a failed attempt can already have published the
// directory (the rename succeeded and the directory fsync failed), which the
// retry adopts by ID, so a smaller retry would not unwrite their rows.
func (w *Writer) commitBatch(ctx context.Context, batch Batch) error {
	started := time.Now()
	var lastErr error
	for attempt := 0; attempt < commitRetryLimit; attempt++ {
		if err := w.repository.Commit(ctx, batch); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
		}
		metrics.FlushErrors.WithLabelValues("batch").Inc()
		slog.Warn("telemetry batch commit failed; retrying", "batch_id", batch.ID, "attempt", attempt+1, "error", lastErr)
		if attempt+1 == commitRetryLimit {
			break
		}
		delay := defaultCommitRetryDelay(attempt)
		if w.retryDelay != nil {
			delay = w.retryDelay(attempt)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr != nil {
		metrics.FlushErrors.WithLabelValues("failed").Inc()
		recordDroppedRows(batch)
		return fmt.Errorf("commit telemetry batch %s after %d attempts: %w", batch.ID, commitRetryLimit, lastErr)
	}
	recordFlushes(batch, time.Since(started).Seconds())
	return nil
}

// maxGroupBatchBytes bounds a group-commit batch by payload rather than row
// count. maxGroupBatchRows caps rows, but a row is whatever the sender put in
// it: 50,000 bare log lines and 50,000 spans carrying kilobytes of attributes
// are the same count and differ by orders of magnitude in what the commit
// holds. The row ceiling alone therefore bounds nothing on a fat-row stream,
// which is precisely the stream that gets the process killed.
const maxGroupBatchBytes = 64 << 20

// batchBytes estimates retained variable-size payloads. A resource object is
// shared within an export, so charge each distinct map only once per request.
// Fixed-width fields and resource references are bounded by the row count.
func batchBytes(batch Batch) int {
	total := 0
	resources := make(map[reflect.Value]struct{})
	resourceBytes := func(resource map[string]any) int {
		if resource == nil {
			return 0
		}
		key := reflect.ValueOf(resource)
		if _, seen := resources[key]; seen {
			return 0
		}
		resources[key] = struct{}{}
		return telemetry.ValueBytes(resource)
	}
	for i := range batch.Spans {
		span := &batch.Spans[i]
		total += resourceBytes(span.Resource) + telemetry.ValueBytes(span.Attributes) + span.ListBytes() +
			len(span.Name) + len(span.StatusMsg) + len(span.TraceID) + len(span.SpanID)
	}
	for i := range batch.Logs {
		log := &batch.Logs[i]
		total += resourceBytes(log.Resource) + telemetry.ValueBytes(log.Attributes) + len(log.Body) + len(log.BodyTemplate)
	}
	for i := range batch.Metrics {
		metric := &batch.Metrics[i]
		total += resourceBytes(metric.Resource) + telemetry.ValueBytes(metric.Attributes) + metric.ListBytes() +
			len(metric.Name) + len(metric.Description)
	}
	return total
}

// groupBatchFits reports whether next can join a group already holding
// groupBytes. An empty group admits anything: one request is already one
// atomic directory, and refusing it would drop data rather than batch it more
// carefully -- the same reason the row ceiling lets a lone oversized request
// through.
func groupBatchFits(groupBytes, nextBytes, groupLen int) bool {
	if groupLen == 0 {
		return true
	}
	return groupBytes <= maxGroupBatchBytes-nextBytes
}

func batchRows(batch Batch) int {
	return len(batch.Spans) + len(batch.Logs) + len(batch.Metrics)
}

func (w *Writer) batchLimit(pressure bool) int {
	if pressure {
		return pressureGroupBatchRows
	}
	limit := min(w.batchSize, maxGroupBatchRows)
	if limit <= 0 {
		return maxGroupBatchRows
	}
	return limit
}

func recordFlushes(batch Batch, durationSec float64) {
	if len(batch.Spans) > 0 {
		metrics.RecordFlush("spans", durationSec)
	}
	if len(batch.Logs) > 0 {
		metrics.RecordFlush("logs", durationSec)
	}
	if len(batch.Metrics) > 0 {
		metrics.RecordFlush("metrics", durationSec)
	}
}

func recordDroppedRows(batch Batch) {
	metrics.RowsDropped.WithLabelValues("spans").Add(float64(len(batch.Spans)))
	metrics.RowsDropped.WithLabelValues("logs").Add(float64(len(batch.Logs)))
	metrics.RowsDropped.WithLabelValues("metrics").Add(float64(len(batch.Metrics)))
}

func defaultCommitRetryDelay(attempt int) time.Duration {
	return min(250*time.Millisecond*time.Duration(1<<min(attempt, 6)), 5*time.Second)
}
