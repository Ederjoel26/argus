package heuristics

import (
	"math"

	"github.com/Ederjoel26/argus/pkg/telemetry"
)

// Aim is the input-telemetry check. Per step between samples, Δθ = angle(forward(r0), forward(r1)) and ω = Δθ/dt.
//
//	snap:     ω_prev > snap_angular_velocity and ω_now < snap_settle_angular_velocity (turn and stop dead, no
//	          overshoot or correction), then a shot within snap_to_fire_ticks. Humans flick fast too; they do not
//	          stop on a dime and fire the same frame.
//	reaction: the target became visible at tick T (server occlusion), the snap came after T and the hit landed
//	          within min_reaction_ms of T. A pre-aimed doorway has no snap after T, so it does not trigger.
//	robotic:  std-dev of the last jitter_window_ticks Δθ before a shot, while the aim was moving, below
//	          jitter_min_stddev_deg. Mouse hands shake; a lerp does not. Only judged for Device == "mouse".
//	silent:   at a hit, the angle between the shooter's view and the server's origin->impact line is beyond the
//	          weapon's spread cone plus silent_aim_max_angle: the bullet went where the crosshair did not.
type Aim struct{}

type aimState struct {
	last        *telemetry.Sample
	prevOmega   float64
	steps       []float64
	snapTick    uint64
	snapOmega   float64
	snapFired   bool
	snapReacted bool
}

// trackingMinMeanDeg: a mean step below this is a still aim, whose jitter is legitimately zero (ours).
const trackingMinMeanDeg = 0.1

func (Aim) Name() string { return "aim" }

func (Aim) Apply(env *Env, st *PlayerState, b *telemetry.Batch) []Finding {
	cfg, a := env.Cfg, &st.Aim
	prev := a.last // the view before this batch, for shots at its first ticks
	var out []Finding

	for i := range b.Samples {
		s := &b.Samples[i]
		idle := s.Flags&(telemetry.FlagDead|telemetry.FlagSpectating) != 0
		if a.last == nil || idle || s.Tick <= a.last.Tick || s.Tick-a.last.Tick > cfg.Movement.MaxGapTicks {
			a.last, a.prevOmega, a.steps = s, 0, a.steps[:0]
			continue
		}
		dt := cfg.Dt(a.last.Tick, s.Tick)
		dtheta := telemetry.AngleDeg(a.last.Rot.Forward(), s.Rot.Forward())
		omega := dtheta / dt
		if n := cfg.Aim.JitterWindowTicks; n > 0 {
			if len(a.steps) == n {
				copy(a.steps, a.steps[1:])
				a.steps = a.steps[:n-1]
			}
			a.steps = append(a.steps, dtheta)
		}
		if a.prevOmega > cfg.Aim.SnapAngularVelocity && omega < cfg.Aim.SnapSettleAngularVelocity {
			a.snapTick, a.snapOmega, a.snapFired, a.snapReacted = a.last.Tick, a.prevOmega, false, false
		}
		a.last, a.prevOmega = s, omega
	}

	for i := range b.Events {
		ev := &b.Events[i]
		switch ev.Type {
		case telemetry.EvFire:
			if a.snapTick > 0 && !a.snapFired && ev.Tick >= a.snapTick && ev.Tick-a.snapTick <= cfg.Aim.SnapToFireTicks {
				a.snapFired = true
				out = append(out, finding("aim.snap", ev.Tick, 0.25+0.75*excess(a.snapOmega, cfg.Aim.SnapAngularVelocity),
					"%.0f deg/s snap, stopped and fired %d ticks later", a.snapOmega, ev.Tick-a.snapTick))
			}
			if b.Device == "mouse" && cfg.Aim.JitterMinStddevDeg > 0 && len(a.steps) == cfg.Aim.JitterWindowTicks {
				if mean, sd := stats(a.steps); mean > trackingMinMeanDeg && sd < cfg.Aim.JitterMinStddevDeg {
					out = append(out, finding("aim.robotic", ev.Tick, below(sd, cfg.Aim.JitterMinStddevDeg),
						"aim std-dev %.4f deg over %d ticks (mean step %.2f)", sd, len(a.steps), mean))
				}
			}
		case telemetry.EvHit:
			view := b.SampleAt(ev.Tick)
			if view == nil {
				view = prev
			}
			if hitDir := ev.Impact.Sub(ev.Origin); view != nil && hitDir.Len() > 0 {
				allowed := cfg.Aim.SilentAimMaxAngle
				if w, ok := cfg.WeaponProfiles[ev.WeaponID]; ok {
					allowed += w.SpreadCone
				}
				if angle := telemetry.AngleDeg(view.Rot.Forward(), hitDir); angle > allowed {
					out = append(out, finding("aim.silent", ev.Tick, excess(angle, allowed),
						"hit %.1f deg off the view (spread+slack %.1f)", angle, allowed))
				}
			}
			if ev.TargetVisibleSince > 0 && a.snapTick >= ev.TargetVisibleSince && !a.snapReacted && ev.Tick >= a.snapTick {
				ms := cfg.Dt(ev.TargetVisibleSince, ev.Tick) * 1000
				if ms < cfg.Aim.MinReactionMs {
					a.snapReacted = true
					out = append(out, finding("aim.reaction", ev.Tick, below(ms, cfg.Aim.MinReactionMs),
						"target seen, snapped and hit in %.0f ms", ms))
				}
			}
		}
	}
	return out
}

func stats(x []float64) (mean, sd float64) {
	for _, v := range x {
		mean += v
	}
	mean /= float64(len(x))
	for _, v := range x {
		sd += (v - mean) * (v - mean)
	}
	return mean, math.Sqrt(sd / float64(len(x)))
}
