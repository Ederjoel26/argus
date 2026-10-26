// Package heuristics holds the checks: pure functions of (config, per-player state, batch) that return findings.
//
// A check never trusts a number the client produced for its own benefit: distances come from the server's shot
// origin and impact, dt from server ticks, visibility from the server's occlusion. Deterministic checks (physics
// made impossible by the config) report confidence 1 at twice the limit; statistical ones report how far a
// behaviour sits outside the human band, and the scorer weighs them lower.
package heuristics

import (
	"fmt"
	"math"

	"github.com/Ederjoel26/argus/pkg/config"
	"github.com/Ederjoel26/argus/pkg/telemetry"
)

// Finding is one anomaly: which check, when, how sure (0..1) and the numbers behind it.
type Finding struct {
	Check      string  `json:"check"`
	Tick       uint64  `json:"tick"`
	Confidence float64 `json:"confidence"`
	Detail     string  `json:"detail"`
}

// World answers geometry questions for the no-clip check. nil World: the check is skipped.
type World interface {
	// Blocked reports whether a straight path from one position to the other crosses solid geometry.
	Blocked(from, to telemetry.Vec3) bool
}

// Env is what every check reads: the game profile and, optionally, the map.
type Env struct {
	Cfg   *config.Config
	World World
}

// Check is one heuristic module. Apply sees a validated batch with only unseen ticks, in order.
type Check interface {
	Name() string
	Apply(env *Env, st *PlayerState, b *telemetry.Batch) []Finding
}

// PlayerState is one player's memory across batches. One goroutine owns it (engine shards by player).
type PlayerState struct {
	PlayerID       string
	LastSampleTick uint64
	LastEventTick  uint64

	Movement movementState
	Aim      aimState
	Weapons  map[string]*weaponState
	Vision   visionState
}

func NewPlayerState(id string) *PlayerState {
	return &PlayerState{PlayerID: id, Weapons: map[string]*weaponState{}, Vision: visionState{targets: map[string]*tracked{}}}
}

// Default is every check, in the order they run.
func Default() []Check { return []Check{Movement{}, Aim{}, Combat{}, Vision{}} }

// Run drops ticks already judged (a retransmitted batch), applies the checks and advances the state's clock.
// A batch holds whole ticks, counted from 1: an event at a tick already judged is a retransmit, not a second shot.
func Run(env *Env, st *PlayerState, b *telemetry.Batch, checks []Check) []Finding {
	b.Samples = fromTick(b.Samples, st.LastSampleTick+1)
	b.Events = fromEventTick(b.Events, st.LastEventTick+1)
	if len(b.Samples) == 0 && len(b.Events) == 0 {
		return nil
	}
	var out []Finding
	for _, c := range checks {
		out = append(out, c.Apply(env, st, b)...)
	}
	if n := len(b.Samples); n > 0 {
		st.LastSampleTick = b.Samples[n-1].Tick
	}
	if n := len(b.Events); n > 0 {
		st.LastEventTick = b.Events[n-1].Tick
	}
	return out
}

func fromTick(s []telemetry.Sample, tick uint64) []telemetry.Sample {
	i := 0
	for i < len(s) && s[i].Tick < tick {
		i++
	}
	return s[i:]
}

func fromEventTick(e []telemetry.Event, tick uint64) []telemetry.Event {
	i := 0
	for i < len(e) && e[i].Tick < tick {
		i++
	}
	return e[i:]
}

// excess is the confidence of exceeding a limit: 0 at the limit, 1 at twice it.
func excess(value, limit float64) float64 {
	if limit <= 0 {
		return 1
	}
	return math.Max(0, math.Min(1, (value-limit)/limit))
}

// below is the confidence of being under a floor: 0 at the floor, 1 at zero.
func below(value, floor float64) float64 {
	if floor <= 0 {
		return 0
	}
	return math.Max(0, math.Min(1, (floor-value)/floor))
}

func finding(check string, tick uint64, conf float64, format string, args ...any) Finding {
	return Finding{Check: check, Tick: tick, Confidence: conf, Detail: fmt.Sprintf(format, args...)}
}
