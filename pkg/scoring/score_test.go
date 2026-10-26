package scoring

import (
	"math"
	"testing"
	"time"

	"github.com/Ederjoel26/argus/pkg/config"
	"github.com/Ederjoel26/argus/pkg/heuristics"
)

func TestScore(t *testing.T) {
	cfg, err := config.Load("../../config/anticheat.config.json")
	if err != nil {
		t.Fatal(err)
	}
	s := New(cfg)
	t0 := time.Unix(1_700_000_000, 0)

	v := s.Apply("p1", t0, []heuristics.Finding{{Check: "movement.teleport", Confidence: 1}})
	if v.Score != 35 || v.Action != Warning || v.Confidence != 1 {
		t.Errorf("one teleport: %+v", v)
	}

	v = s.Apply("p1", t0, []heuristics.Finding{{Check: "aim.robotic", Confidence: 0.5}}) // 35 + 8 * 0.5
	if v.Score != 39 || v.Action != Warning {
		t.Errorf("plus a weak statistic: %+v", v)
	}

	v = s.Apply("p1", t0.Add(2*time.Minute), nil) // one half-life, clean
	if math.Abs(v.Score-19.5) > 1e-9 || v.Action != None || v.Confidence >= 1 || v.Confidence < 0.5 {
		t.Errorf("after a half-life: %+v", v)
	}

	for i := 0; i < 3; i++ {
		v = s.Apply("p1", t0.Add(2*time.Minute), []heuristics.Finding{{Check: "combat.damage", Confidence: 1}})
	}
	if v.Score != cfg.Scoring.MaxScore || v.Action != Disconnect {
		t.Errorf("three impossible damages: %+v", v)
	}
	if len(v.Findings) != 5 {
		t.Errorf("recent findings: %d", len(v.Findings))
	}

	s.Forget("p1")
	if v = s.Apply("p1", t0.Add(3*time.Minute), nil); v.Score != 0 || v.Action != None {
		t.Errorf("forgotten: %+v", v)
	}
}
