package heuristics

import "github.com/Ederjoel26/argus/pkg/telemetry"

// Movement is the kinematic check. For each pair of consecutive samples (dt = tick delta / tick_rate):
//
//	v_h = |Δxz| / dt              > v_max (1+tol)            speeding       (v_max by state: on foot, vehicle, gliding)
//	|Δxz| > factor · v_max · dt                              teleport       (one step, not a trend)
//	v_y  = Δy / dt               > rise_max or < -fall_max    vertical       (fly hack, no-clip through the floor)
//	a_h  = (v_h - v_h,prev) / dt > a_max (1+tol)             acceleration   (only speeding up: braking may be instant)
//	World.Blocked(p0, p1)                                    noclip         (when a World is given)
//
// Speeding needs min_violations consecutive bad steps so one interpolated or lost packet is forgiven; the three
// others are one-step findings. A set FlagTeleport (spawn, knockback, taxi drop), a gap longer than max_gap_ticks
// (reconnect) or a dead/spectating player reset the history instead of being judged.
type Movement struct{}

type movementState struct {
	last       *telemetry.Sample
	prevSpeed  float64
	speedKnown bool // false right after a reset: the first step has no previous speed to accelerate from
	violations int
}

func (Movement) Name() string { return "movement" }

func (Movement) Apply(env *Env, st *PlayerState, b *telemetry.Batch) []Finding {
	cfg := env.Cfg
	m := &st.Movement
	var out []Finding
	for i := range b.Samples {
		s := &b.Samples[i]
		skip := s.Flags&(telemetry.FlagTeleport|telemetry.FlagDead|telemetry.FlagSpectating) != 0
		if m.last == nil || skip || s.Tick <= m.last.Tick || s.Tick-m.last.Tick > cfg.Movement.MaxGapTicks {
			m.last, m.speedKnown, m.violations = s, false, 0
			continue
		}
		dt := cfg.Dt(m.last.Tick, s.Tick)
		d := s.Pos.Sub(m.last.Pos)
		vh, vy := d.Horizontal()/dt, d.Y/dt
		vmax := cfg.Movement.MaxPlayerVelocity
		switch {
		case s.Flags&telemetry.FlagInVehicle != 0:
			vmax = cfg.Movement.MaxVehicleVelocity
		case s.Flags&telemetry.FlagGliding != 0:
			vmax = cfg.Movement.MaxGlideVelocity
		}
		tol := 1 + cfg.Movement.Tolerance

		if d.Horizontal() > cfg.Movement.TeleportFactor*vmax*dt {
			out = append(out, finding("movement.teleport", s.Tick, 1,
				"moved %.1f u in %.0f ms (limit %.1f u)", d.Horizontal(), dt*1000, vmax*dt))
			m.last, m.speedKnown, m.violations = s, false, 0
			continue
		}
		if vh > vmax*tol {
			m.violations++
			if m.violations >= cfg.Movement.MinViolations {
				out = append(out, finding("movement.speed", s.Tick, excess(vh, vmax),
					"%.1f u/s over %d ticks (limit %.1f)", vh, m.violations, vmax))
				m.violations = 0
			}
		} else if m.violations > 0 {
			m.violations--
		}
		if s.Flags&(telemetry.FlagInVehicle|telemetry.FlagGliding) == 0 {
			if rise := cfg.Movement.MaxRiseVelocity; rise > 0 && vy > rise*tol {
				out = append(out, finding("movement.vertical", s.Tick, excess(vy, rise), "rising at %.1f u/s (limit %.1f)", vy, rise))
			}
		}
		if fall := cfg.Movement.MaxFallVelocity; fall > 0 && -vy > fall*tol {
			out = append(out, finding("movement.vertical", s.Tick, excess(-vy, fall), "falling at %.1f u/s (limit %.1f)", -vy, fall))
		}
		if a := (vh - m.prevSpeed) / dt; m.speedKnown && cfg.Movement.AccelerationThreshold > 0 && a > cfg.Movement.AccelerationThreshold*tol {
			out = append(out, finding("movement.acceleration", s.Tick, excess(a, cfg.Movement.AccelerationThreshold),
				"%.0f u/s² from %.1f to %.1f u/s", a, m.prevSpeed, vh))
		}
		if env.World != nil && env.World.Blocked(m.last.Pos, s.Pos) {
			out = append(out, finding("movement.noclip", s.Tick, 1, "path %v -> %v crosses geometry", m.last.Pos, s.Pos))
		}
		m.last, m.prevSpeed, m.speedKnown = s, vh, true
	}
	return out
}
