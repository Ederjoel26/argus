// Package engine is the ingest pipeline: batches are validated at the boundary, sharded by player onto a worker
// pool, judged in order by the heuristics, scored, and the verdicts published to a Sink.
//
// Sharding by player is what makes the state machines correct without locks: one goroutine owns a player's
// PlayerState and scores, and sees its batches in submission order. Scale out the same way: shard players across
// processes by the same hash (a Redis Stream or Kafka partition per shard), each process running this Engine.
package engine

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ederjoel26/argus/pkg/config"
	"github.com/Ederjoel26/argus/pkg/heuristics"
	"github.com/Ederjoel26/argus/pkg/scoring"
	"github.com/Ederjoel26/argus/pkg/telemetry"
)

// Sink receives every verdict. A Kafka topic, a Redis Stream, the game server's ban hook: implement this.
type Sink interface {
	Publish(scoring.Verdict)
}

type Options struct {
	Workers    int                // shards; default: one per CPU
	QueueDepth int                // batches buffered per shard before Submit blocks; default 1024
	World      heuristics.World   // map geometry for the no-clip check; nil skips it
	Checks     []heuristics.Check // default: heuristics.Default()
	Now        func() time.Time   // default: time.Now
}

type Engine struct {
	env    heuristics.Env
	opts   Options
	sink   Sink
	shards []chan job
	wg     sync.WaitGroup

	Accepted atomic.Uint64
	Rejected atomic.Uint64
}

type job struct {
	batch  *telemetry.Batch
	forget string
}

var ErrStopped = errors.New("engine: stopped")

func New(cfg *config.Config, sink Sink, opts Options) *Engine {
	if opts.Workers <= 0 {
		opts.Workers = runtime.NumCPU()
	}
	if opts.QueueDepth <= 0 {
		opts.QueueDepth = 1024
	}
	if opts.Checks == nil {
		opts.Checks = heuristics.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	e := &Engine{env: heuristics.Env{Cfg: cfg, World: opts.World}, opts: opts, sink: sink}
	for i := 0; i < opts.Workers; i++ {
		e.shards = append(e.shards, make(chan job, opts.QueueDepth))
	}
	return e
}

// Start runs the workers until ctx is cancelled; Wait blocks until they have stopped.
func (e *Engine) Start(ctx context.Context) {
	for _, ch := range e.shards {
		e.wg.Add(1)
		go e.worker(ctx, ch)
	}
}

func (e *Engine) Wait() { e.wg.Wait() }

// Submit validates a batch and queues it for its player's shard; it blocks while that shard's queue is full (back
// pressure to the transport) and gives up when ctx ends.
func (e *Engine) Submit(ctx context.Context, b *telemetry.Batch) error {
	if err := b.Validate(); err != nil {
		e.Rejected.Add(1)
		return err
	}
	if err := e.enqueue(ctx, b.PlayerID, job{batch: b}); err != nil {
		return err
	}
	e.Accepted.Add(1)
	return nil
}

// Forget drops a player's state and score (match over).
func (e *Engine) Forget(ctx context.Context, playerID string) error {
	return e.enqueue(ctx, playerID, job{forget: playerID})
}

func (e *Engine) enqueue(ctx context.Context, playerID string, j job) error {
	h := fnv.New32a()
	h.Write([]byte(playerID))
	select {
	case e.shards[h.Sum32()%uint32(len(e.shards))] <- j:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) worker(ctx context.Context, jobs <-chan job) {
	defer e.wg.Done()
	states := map[string]*heuristics.PlayerState{}
	scorer := scoring.New(e.env.Cfg)
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-jobs:
			if j.forget != "" {
				delete(states, j.forget)
				scorer.Forget(j.forget)
				continue
			}
			id := j.batch.PlayerID
			st := states[id]
			if st == nil {
				st = heuristics.NewPlayerState(id)
				states[id] = st
			}
			findings := heuristics.Run(&e.env, st, j.batch, e.opts.Checks)
			e.sink.Publish(scorer.Apply(id, e.opts.Now(), findings))
		}
	}
}

// MemorySink keeps the latest verdict per player, for an API to read.
type MemorySink struct {
	mu       sync.RWMutex
	verdicts map[string]scoring.Verdict
}

func NewMemorySink() *MemorySink { return &MemorySink{verdicts: map[string]scoring.Verdict{}} }

func (m *MemorySink) Publish(v scoring.Verdict) {
	m.mu.Lock()
	m.verdicts[v.PlayerID] = v
	m.mu.Unlock()
}

func (m *MemorySink) Get(playerID string) (scoring.Verdict, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.verdicts[playerID]
	return v, ok
}

// LogSink logs every verdict that carries a finding or an action.
type LogSink struct{ Log *slog.Logger }

func (l LogSink) Publish(v scoring.Verdict) {
	if len(v.Findings) == 0 && v.Action == scoring.None {
		return
	}
	last := v.Findings[len(v.Findings)-1]
	l.Log.Info("verdict", "player", v.PlayerID, "score", v.Score, "confidence", v.Confidence, "action", v.Action,
		"last_check", last.Check, "detail", last.Detail)
}

// MultiSink publishes to several sinks in order.
type MultiSink []Sink

func (m MultiSink) Publish(v scoring.Verdict) {
	for _, s := range m {
		s.Publish(v)
	}
}
