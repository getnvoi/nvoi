package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	app "github.com/getnvoi/nvoi/pkg/core"
)

var (
	reporterBufSize    = 4096             // max buffered events before dropping oldest
	reporterBatchSize  = 100              // max events per POST
	reporterFlushEvery = 2 * time.Second  // flush interval
	reporterBackoffMin = 1 * time.Second  // initial backoff
	reporterBackoffMax = 30 * time.Second // max backoff
)

// Reporter sends events to the API asynchronously. Non-blocking — deploys
// never stall on API latency. Bounded ring buffer drops oldest events if
// the API is unreachable for too long.
type Reporter struct {
	url    string // {apiURL}/agent/events
	token  string // workspace-scoped bearer token
	app    string // app name for identification
	env    string // env name for identification
	client *http.Client

	ch   chan app.Event
	done chan struct{}
	wg   sync.WaitGroup
}

// NewReporter starts a background goroutine that batches and sends events.
// Returns nil if url is empty (standalone mode — no API).
func NewReporter(url, token, appName, env string) *Reporter {
	if url == "" {
		return nil
	}
	r := &Reporter{
		url:    url + "/agent/events",
		token:  token,
		app:    appName,
		env:    env,
		client: &http.Client{Timeout: 10 * time.Second},
		ch:     make(chan app.Event, reporterBufSize),
		done:   make(chan struct{}),
	}
	r.wg.Add(1)
	go r.loop()
	return r
}

// Send enqueues an event. Non-blocking — drops silently if buffer is full.
func (r *Reporter) Send(ev app.Event) {
	select {
	case r.ch <- ev:
	default:
		// Buffer full — drop oldest by reading one, then retry.
		select {
		case <-r.ch:
		default:
		}
		select {
		case r.ch <- ev:
		default:
		}
	}
}

// Close flushes remaining events and shuts down the reporter.
func (r *Reporter) Close() {
	close(r.done)
	r.wg.Wait()
}

func (r *Reporter) loop() {
	defer r.wg.Done()
	ticker := time.NewTicker(reporterFlushEvery)
	defer ticker.Stop()

	var batch []app.Event
	backoff := reporterBackoffMin
	var backoffTimer <-chan time.Time // nil until a send fails

	for {
		select {
		case ev := <-r.ch:
			batch = append(batch, ev)
			if len(batch) >= reporterBatchSize && backoffTimer == nil {
				if r.send(batch) {
					backoff = reporterBackoffMin
				} else {
					backoff = nextBackoff(backoff)
					backoffTimer = time.After(backoff)
				}
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 && backoffTimer == nil {
				if r.send(batch) {
					backoff = reporterBackoffMin
				} else {
					backoff = nextBackoff(backoff)
					backoffTimer = time.After(backoff)
				}
				batch = batch[:0]
			}

		case <-backoffTimer:
			// Backoff elapsed — retry pending batch or resume normal flushing.
			backoffTimer = nil
			if len(batch) > 0 {
				if r.send(batch) {
					backoff = reporterBackoffMin
				} else {
					backoff = nextBackoff(backoff)
					backoffTimer = time.After(backoff)
				}
				batch = batch[:0]
			}

		case <-r.done:
			// Drain remaining channel events.
			for {
				select {
				case ev := <-r.ch:
					batch = append(batch, ev)
				default:
					goto flush
				}
			}
		flush:
			if len(batch) > 0 {
				r.send(batch)
			}
			return
		}
	}
}

func (r *Reporter) send(events []app.Event) bool {
	payload := struct {
		App    string      `json:"app"`
		Env    string      `json:"env"`
		Events []app.Event `json:"events"`
	}{
		App:    r.app,
		Env:    r.env,
		Events: events,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}

	req, err := http.NewRequest("POST", r.url, bytes.NewReader(data))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return false
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > reporterBackoffMax {
		return reporterBackoffMax
	}
	return next
}

// ── teeOutput ──────────────────────────────────────────────────────────────

// teeOutput wraps an Output and forwards every event to a Reporter.
// The primary output (JSONL to CLI) is always written. The reporter
// is fire-and-forget — API failures never affect the deploy.
type teeOutput struct {
	primary app.Output
	r       *Reporter
}

func newTeeOutput(primary app.Output, r *Reporter) app.Output {
	if r == nil {
		return primary
	}
	return &teeOutput{primary: primary, r: r}
}

func (t *teeOutput) Command(command, action, name string, extra ...any) {
	t.primary.Command(command, action, name, extra...)
	t.r.Send(app.NewCommandEvent(command, action, name, extra...))
}

func (t *teeOutput) Progress(msg string) {
	t.primary.Progress(msg)
	t.r.Send(app.NewMessageEvent(app.EventProgress, msg))
}

func (t *teeOutput) Success(msg string) {
	t.primary.Success(msg)
	t.r.Send(app.NewMessageEvent(app.EventSuccess, msg))
}

func (t *teeOutput) Warning(msg string) {
	t.primary.Warning(msg)
	t.r.Send(app.NewMessageEvent(app.EventWarning, msg))
}

func (t *teeOutput) Info(msg string) {
	t.primary.Info(msg)
	t.r.Send(app.NewMessageEvent(app.EventInfo, msg))
}

func (t *teeOutput) Error(err error) {
	t.primary.Error(err)
	t.r.Send(app.NewMessageEvent(app.EventError, err.Error()))
}

func (t *teeOutput) Writer() io.Writer {
	return &teeWriter{primary: t.primary.Writer(), r: t.r}
}

// teeWriter forwards writes to both the primary writer and the reporter.
type teeWriter struct {
	primary io.Writer
	r       *Reporter
}

func (tw *teeWriter) Write(p []byte) (int, error) {
	n, err := tw.primary.Write(p)
	tw.r.Send(app.NewMessageEvent(app.EventStream, string(p)))
	return n, err
}
