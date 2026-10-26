// Package config is the declarative game profile: everything a heuristic compares against comes from here, so the
// engine itself knows nothing about a particular game. config/anticheat.config.json is the annotated example.
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	TickRate float64 `json:"tick_rate"` // game-server ticks a second; all dt come from tick deltas, never wall clocks

	Movement Movement `json:"movement"`
	Aim      Aim      `json:"aim"`
	Combat   Combat   `json:"combat"`
	Vision   Vision   `json:"vision"`

	WeaponProfiles map[string]Weapon `json:"weapon_profiles"`

	Scoring           Scoring            `json:"scoring"`
	PenaltyThresholds PenaltyThresholds  `json:"penalty_thresholds"`
	CheckWeights      map[string]float64 `json:"check_weights,omitempty"` // score a finding of this check adds at confidence 1; defaults in scoring
}

// Movement: world units a second (and a second squared). Horizontal and vertical motion are checked apart, because
// a fall is fast and legal while a sprint is slow.
type Movement struct {
	MaxPlayerVelocity     float64 `json:"max_player_velocity"`
	MaxVehicleVelocity    float64 `json:"max_vehicle_velocity"`
	MaxGlideVelocity      float64 `json:"max_glide_velocity"`
	MaxRiseVelocity       float64 `json:"max_rise_velocity"` // a jump's take-off speed: sqrt(2 g h)
	MaxFallVelocity       float64 `json:"max_fall_velocity"` // terminal velocity
	AccelerationThreshold float64 `json:"acceleration_threshold"`
	Tolerance             float64 `json:"tolerance"`       // fraction of slack over every limit, for tick jitter and interpolation
	TeleportFactor        float64 `json:"teleport_factor"` // one step longer than factor * limit * dt is a teleport, not speeding
	MaxGapTicks           uint64  `json:"max_gap_ticks"`   // a longer silence (reconnect) resets the history instead of being judged
	MinViolations         int     `json:"min_violations"`  // speeding steps in a row before one finding
}

type Aim struct {
	SnapAngularVelocity       float64 `json:"snap_angular_velocity"`        // deg/s: faster than this is a snap...
	SnapSettleAngularVelocity float64 `json:"snap_settle_angular_velocity"` // ...when the next tick is slower than this (snap and stop)
	SnapToFireTicks           uint64  `json:"snap_to_fire_ticks"`           // and a shot follows within this many ticks
	MinReactionMs             float64 `json:"min_reaction_ms"`              // target appears -> snap -> hit faster than this is inhuman
	JitterWindowTicks         int     `json:"jitter_window_ticks"`          // angular steps before a shot that the jitter statistic sees
	JitterMinStddevDeg        float64 `json:"jitter_min_stddev_deg"`        // mouse aim below this std-dev while moving is robotic (0: off)
	SilentAimMaxAngle         float64 `json:"silent_aim_max_angle"`         // deg beyond the weapon's spread cone between view and hit
}

type Combat struct {
	FireRateToleranceTicks uint64  `json:"fire_rate_tolerance_ticks"` // tick quantization slack on the minimum interval
	RangeTolerance         float64 `json:"range_tolerance"`           // fraction over max_range
	DamageTolerance        float64 `json:"damage_tolerance"`          // fraction over max_damage (float rounding)
}

type Vision struct {
	PreaimAngle   float64 `json:"preaim_angle"`    // deg: crosshair this close to an occluded enemy counts as aimed at it
	PreaimTicks   int     `json:"preaim_ticks"`    // ticks of tracking a moving, occluded enemy before one finding
	MinTargetMove float64 `json:"min_target_move"` // world units the enemy must move between ticks to count as "tracked"
	MaxDistance   float64 `json:"max_distance"`    // enemies farther than this are not judged
}

type Weapon struct {
	FireRate           float64 `json:"fire_rate"` // trigger pulls a second
	MaxRange           float64 `json:"max_range"`
	SpreadCone         float64 `json:"spread_cone"` // deg, half-angle
	MaxDamage          float64 `json:"max_damage"`
	HeadshotMultiplier float64 `json:"headshot_multiplier"`
	Magazine           int     `json:"magazine"`
	ReloadMs           float64 `json:"reload_ms"`
}

type Scoring struct {
	HalfLifeSeconds float64 `json:"half_life_s"` // the score halves every this many seconds without findings
	MaxScore        float64 `json:"max_score"`
	RecentFindings  int     `json:"recent_findings"` // findings kept per player for the confidence estimate
}

// PenaltyThresholds are scores; the highest one reached is the action.
type PenaltyThresholds struct {
	Warning    float64 `json:"warning"`
	Shadowban  float64 `json:"shadowban"`
	Kick       float64 `json:"kick"`
	Disconnect float64 `json:"disconnect"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &c, c.Validate()
}

// Validate catches the config errors that would otherwise make every check silently pass or divide by zero.
func (c *Config) Validate() error {
	switch {
	case c.TickRate <= 0:
		return fmt.Errorf("config: tick_rate must be > 0")
	case c.Movement.MaxPlayerVelocity <= 0:
		return fmt.Errorf("config: movement.max_player_velocity must be > 0")
	case c.Movement.TeleportFactor < 1:
		return fmt.Errorf("config: movement.teleport_factor must be >= 1")
	case c.Scoring.MaxScore <= 0 || c.Scoring.HalfLifeSeconds <= 0:
		return fmt.Errorf("config: scoring.max_score and half_life_s must be > 0")
	}
	for id, w := range c.WeaponProfiles {
		if w.FireRate <= 0 || w.MaxRange <= 0 || w.MaxDamage <= 0 {
			return fmt.Errorf("config: weapon %q needs fire_rate, max_range and max_damage > 0", id)
		}
	}
	if c.Movement.MinViolations < 1 {
		c.Movement.MinViolations = 1
	}
	if c.Scoring.RecentFindings < 1 {
		c.Scoring.RecentFindings = 32
	}
	return nil
}

// Dt is the seconds between two ticks.
func (c *Config) Dt(from, to uint64) float64 { return float64(to-from) / c.TickRate }

// Ticks is how many ticks a duration in milliseconds spans, rounded up.
func (c *Config) Ticks(ms float64) uint64 { return uint64(ms*c.TickRate/1000 + 0.999) }
