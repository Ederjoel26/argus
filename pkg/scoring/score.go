// Package scoring turns findings into one decaying anomaly score per player, a confidence, and an action.
//
//	score      += weight(check) · confidence          for each finding, capped at max_score
//	score      *= 0.5 ^ (Δt / half_life_s)            between batches: a clean player drifts back to 0
//	confidence  = 1 - Π (1 - c_i · decay_i)           noisy-OR over the recent findings: how sure that *something*
//	                                                  is wrong, independent of how much it weighs
//	action      = the highest penalty threshold the score reached
//
// Weights say how damning a check is: physics the config makes impossible (teleport, fire rate, damage) is worth a
// penalty in a few hits; statistics (jitter, pre-aim) need a pattern. The config's check_weights override these.
package scoring

import (
	"math"
	"time"

	"github.com/Ederjoel26/argus/pkg/config"
	"github.com/Ederjoel26/argus/pkg/heuristics"
)

type Action string

const (
	None       Action = "none"
	Warning    Action = "warning"
	Shadowban  Action = "shadowban"
	Kick       Action = "kick"
	Disconnect Action = "disconnect"
)

// DefaultWeights: score at confidence 1, when the config names none for a check.
var DefaultWeights = map[string]float64{
	"movement.teleport": 35, "movement.noclip": 30, "movement.speed": 20, "movement.vertical": 20, "movement.acceleration": 12,
	"aim.silent": 35, "aim.snap": 15, "aim.reaction": 12, "aim.robotic": 8,
	"combat.unknown_weapon": 40, "combat.damage": 35, "combat.fire_rate": 30, "combat.reload": 30, "combat.magazine": 30, "combat.range": 25,
	"vision.preaim": 10,
}

const unknownCheckWeight = 10

// Verdict is a player's standing after a batch.
type Verdict struct {
	PlayerID   string               `json:"player_id"`
	Score      float64              `json:"score"`
	Confidence float64              `json:"confidence"`
	Action     Action               `json:"action"`
	UpdatedAt  time.Time            `json:"updated_at"`
	Findings   []heuristics.Finding `json:"findings"` // the recent ones, newest last
}

type Scorer struct {
	cfg     *config.Config
	players map[string]*player
}

type player struct {
	score   float64
	updated time.Time
	recent  []dated
}

type dated struct {
	heuristics.Finding
	at time.Time
}

func New(cfg *config.Config) *Scorer { return &Scorer{cfg: cfg, players: map[string]*player{}} }

// Apply adds a batch's findings (possibly none: the decay still runs) and returns the player's verdict.
func (s *Scorer) Apply(id string, now time.Time, fs []heuristics.Finding) Verdict {
	p := s.players[id]
	if p == nil {
		p = &player{updated: now}
		s.players[id] = p
	}
	p.score *= s.decay(now.Sub(p.updated))
	p.updated = now
	for _, f := range fs {
		p.score += s.weight(f.Check) * f.Confidence
		p.recent = append(p.recent, dated{f, now})
	}
	p.score = math.Min(p.score, s.cfg.Scoring.MaxScore)
	if extra := len(p.recent) - s.cfg.Scoring.RecentFindings; extra > 0 {
		p.recent = append(p.recent[:0], p.recent[extra:]...)
	}

	v := Verdict{PlayerID: id, Score: p.score, Action: s.action(p.score), UpdatedAt: now}
	notWrong := 1.0
	for _, d := range p.recent {
		notWrong *= 1 - d.Confidence*s.decay(now.Sub(d.at))
		v.Findings = append(v.Findings, d.Finding)
	}
	v.Confidence = 1 - notWrong
	return v
}

// Forget drops a player (match over, account reviewed).
func (s *Scorer) Forget(id string) { delete(s.players, id) }

func (s *Scorer) decay(age time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, age.Seconds()/s.cfg.Scoring.HalfLifeSeconds)
}

func (s *Scorer) weight(check string) float64 {
	if w, ok := s.cfg.CheckWeights[check]; ok {
		return w
	}
	if w, ok := DefaultWeights[check]; ok {
		return w
	}
	return unknownCheckWeight
}

func (s *Scorer) action(score float64) Action {
	t := s.cfg.PenaltyThresholds
	switch {
	case t.Disconnect > 0 && score >= t.Disconnect:
		return Disconnect
	case t.Kick > 0 && score >= t.Kick:
		return Kick
	case t.Shadowban > 0 && score >= t.Shadowban:
		return Shadowban
	case t.Warning > 0 && score >= t.Warning:
		return Warning
	}
	return None
}
