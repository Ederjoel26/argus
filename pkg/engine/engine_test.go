package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Ederjoel26/argus/pkg/config"
	"github.com/Ederjoel26/argus/pkg/scoring"
	"github.com/Ederjoel26/argus/pkg/telemetry"
)

// Batches of one player are judged in order on one shard, whatever the worker count: the second batch's speed
// hack is seen against the first batch's last sample, and the replayed first batch adds nothing.
func TestEngineOrdersPerPlayer(t *testing.T) {
	cfg, err := config.Load("../../config/anticheat.config.json")
	if err != nil {
		t.Fatal(err)
	}
	sink := NewMemorySink()
	done := make(chan scoring.Verdict, 16)
	eng := New(cfg, MultiSink{sink, sinkFunc(func(v scoring.Verdict) { done <- v })}, Options{Workers: 4, QueueDepth: 8})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(ctx)

	walk := func(from, n int, startX, speed float64) []telemetry.Sample {
		s := make([]telemetry.Sample, n)
		for i := range s {
			s[i] = telemetry.Sample{Tick: uint64(from + i), Pos: telemetry.Vec3{X: startX + speed*0.05*float64(i)}, Flags: telemetry.FlagGrounded}
		}
		return s
	}
	first := &telemetry.Batch{PlayerID: "p1", Samples: walk(1, 20, 0, 56)}
	second := &telemetry.Batch{PlayerID: "p1", Samples: walk(21, 20, 56*0.05*20, 120)} // on from where the first ended, twice too fast
	for _, b := range []*telemetry.Batch{first, second, {PlayerID: "p1", Samples: walk(1, 20, 0, 56)}} {
		if err := eng.Submit(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng.Submit(ctx, &telemetry.Batch{Samples: walk(1, 5, 0, 1)}); err != telemetry.ErrNoPlayer {
		t.Errorf("anonymous batch: %v", err)
	}

	var last scoring.Verdict
	for i := 0; i < 3; i++ {
		select {
		case last = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("no verdict")
		}
	}
	got, _ := sink.Get("p1")
	if got.Score != last.Score || got.Score <= 0 || got.Findings[0].Check != "movement.speed" {
		t.Errorf("verdict %+v", got)
	}
	if n := len(got.Findings); n != 6 { // 20 speeding ticks, a finding every 3: not more from the replay
		t.Errorf("%d findings, want 6", n)
	}
	if eng.Accepted.Load() != 3 || eng.Rejected.Load() != 1 {
		t.Errorf("accepted %d rejected %d", eng.Accepted.Load(), eng.Rejected.Load())
	}

	if err := eng.Forget(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	cancel()
	eng.Wait()
}

type sinkFunc func(scoring.Verdict)

func (f sinkFunc) Publish(v scoring.Verdict) { f(v) }
