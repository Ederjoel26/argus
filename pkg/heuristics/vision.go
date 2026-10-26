package heuristics

import "github.com/Ederjoel26/argus/pkg/telemetry"

// Vision is the wallhack heuristic. Wallhacks are *prevented* in the game server, by not sending what a player
// cannot see (README: interest management); this check finds the ones that slip through the margin by what they
// do with the knowledge: tracking.
//
//	preaim: for each enemy the server marks occluded, a tick counts when the enemy moved at least min_target_move
//	        since the previous tick and the crosshair stayed within preaim_angle of it. Holding an angle at a wall
//	        an enemy happens to stand behind does not count (it does not move); following a target through the
//	        wall does. preaim_ticks such ticks make one finding, then the count restarts. A visible enemy, or one
//	        past max_distance, resets its count.
type Vision struct{}

type visionState struct {
	targets map[string]*tracked
}

type tracked struct {
	lastPos  telemetry.Vec3
	lastTick uint64
	ticks    int
	angleSum float64
}

func (Vision) Name() string { return "vision" }

func (Vision) Apply(env *Env, st *PlayerState, b *telemetry.Batch) []Finding {
	cfg, v := env.Cfg, &st.Vision
	if v.targets == nil {
		v.targets = map[string]*tracked{}
	}
	var out []Finding
	for i := range b.Samples {
		s := &b.Samples[i]
		fwd := s.Rot.Forward()
		for _, c := range s.Contacts {
			t := v.targets[c.ID]
			if t == nil {
				t = &tracked{}
				v.targets[c.ID] = t
			}
			toTarget := c.Pos.Sub(s.Pos)
			known := t.lastTick != 0 && s.Tick > t.lastTick && s.Tick-t.lastTick <= cfg.Movement.MaxGapTicks
			switch {
			case c.Visible || toTarget.Len() > cfg.Vision.MaxDistance:
				t.ticks, t.angleSum = 0, 0
			case known && c.Pos.Sub(t.lastPos).Len() >= cfg.Vision.MinTargetMove && telemetry.AngleDeg(fwd, toTarget) <= cfg.Vision.PreaimAngle:
				t.ticks++
				t.angleSum += telemetry.AngleDeg(fwd, toTarget)
				if t.ticks >= cfg.Vision.PreaimTicks && cfg.Vision.PreaimTicks > 0 {
					mean := t.angleSum / float64(t.ticks)
					out = append(out, finding("vision.preaim", s.Tick, 0.5+0.5*(1-mean/cfg.Vision.PreaimAngle),
						"tracked occluded %s for %d ticks, %.1f deg off on average", c.ID, t.ticks, mean))
					t.ticks, t.angleSum = 0, 0
				}
			case t.ticks > 0:
				t.ticks-- // one tick off target is forgiven, a trend is not
			}
			t.lastPos, t.lastTick = c.Pos, s.Tick
		}
	}
	return out
}
