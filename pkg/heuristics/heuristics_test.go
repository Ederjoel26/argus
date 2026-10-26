package heuristics

import (
	"math"
	"math/rand"
	"testing"

	"github.com/Ederjoel26/argus/pkg/config"
	"github.com/Ederjoel26/argus/pkg/telemetry"
)

// The checks against synthetic traces at the example config (20 Hz, sprint 56 u/s): a legit player yields nothing,
// each cheat yields its finding.

func env(t *testing.T) *Env {
	t.Helper()
	cfg, err := config.Load("../../config/anticheat.config.json")
	if err != nil {
		t.Fatal(err)
	}
	return &Env{Cfg: cfg}
}

func run(t *testing.T, e *Env, st *PlayerState, b telemetry.Batch) map[string][]Finding {
	t.Helper()
	if b.PlayerID == "" {
		b.PlayerID = "p1"
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	out := map[string][]Finding{}
	for _, f := range Run(e, st, &b, Default()) {
		out[f.Check] = append(out[f.Check], f)
	}
	return out
}

func want(t *testing.T, got map[string][]Finding, checks ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, c := range checks {
		seen[c] = true
		if len(got[c]) == 0 {
			t.Errorf("expected %s, got %v", c, got)
		}
	}
	for c, fs := range got {
		if !seen[c] {
			t.Errorf("unexpected %s: %+v", c, fs[0])
		}
	}
}

// walk: n grounded samples from tick 1 moving along +X at speed u/s, looking along +Z.
func walk(n int, speed float64) []telemetry.Sample {
	s := make([]telemetry.Sample, n)
	for i := range s {
		s[i] = telemetry.Sample{Tick: uint64(i + 1), Pos: telemetry.Vec3{X: speed * 0.05 * float64(i)}, Flags: telemetry.FlagGrounded}
	}
	return s
}

func TestMovement(t *testing.T) {
	e := env(t)
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: walk(40, 56)})) // a sprint: clean

	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: walk(40, 100)}), "movement.speed")

	jump := func() []telemetry.Sample { // 50 u ahead in one tick, then on as before
		s := walk(10, 56)
		for i := 5; i < 10; i++ {
			s[i].Pos.X += 50
		}
		return s
	}
	got := run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: jump()})
	want(t, got, "movement.teleport")
	if n := len(got["movement.teleport"]); n != 1 || got["movement.teleport"][0].Confidence != 1 {
		t.Errorf("one certain teleport, got %+v", got["movement.teleport"])
	}

	s := jump()
	s[5].Flags |= telemetry.FlagTeleport // the server moved them: a taxi drop, a respawn
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: s}))

	s = walk(10, 0)
	for i := range s {
		s[i].Pos.Y = 10 * float64(i) // rising 200 u/s on foot
	}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: s}), "movement.vertical")

	s = walk(10, 0)
	for i := range s {
		s[i].Pos.Y = -40 * float64(i) // falling 800 u/s, past terminal velocity
	}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: s}), "movement.vertical")

	s = walk(10, 0)
	for i := 5; i < 10; i++ {
		s[i].Pos.X = 6 * float64(i-4) // standing, then 120 u/s in one tick: too fast, and too sudden
	}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: s}), "movement.speed", "movement.acceleration")

	s = walk(10, 250)
	for i := range s {
		s[i].Flags = telemetry.FlagInVehicle
	}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: s})) // 250 u/s is a car

	s = walk(10, 56)
	s[5].Tick += 100 // a reconnect: the gap is not judged
	s[5].Pos.X += 500
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: s}))
}

type wall struct{ x float64 }

func (w wall) Blocked(from, to telemetry.Vec3) bool { return (from.X < w.x) != (to.X < w.x) }

func TestNoClip(t *testing.T) {
	e := env(t)
	e.World = wall{x: 3}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: walk(10, 56)}), "movement.noclip")
}

func aimTrace(n int, yaw func(i int) float64) []telemetry.Sample {
	s := make([]telemetry.Sample, n)
	for i := range s {
		s[i] = telemetry.Sample{Tick: uint64(i + 1), Rot: telemetry.Rot{Yaw: yaw(i)}, Flags: telemetry.FlagGrounded}
	}
	return s
}

func TestAimSnap(t *testing.T) {
	e := env(t)
	snap := aimTrace(20, func(i int) float64 { // still, then 90 deg in one tick, then still
		if i >= 10 {
			return 90
		}
		return 0
	})
	fire := []telemetry.Event{{Tick: 12, Type: telemetry.EvFire, WeaponID: "rifle"}}
	got := run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: snap, Events: fire})
	want(t, got, "aim.snap")
	if c := got["aim.snap"][0].Confidence; math.Abs(c-0.4) > 1e-9 {
		t.Errorf("1800 deg/s at a 1500 limit: confidence %.2f, want 0.40", c)
	}

	// a human flick: as fast, but overshoots and corrects over the next ticks
	human := aimTrace(20, func(i int) float64 {
		switch {
		case i < 10:
			return 0
		case i == 10:
			return 96
		case i == 11:
			return 92
		case i == 12:
			return 89.5
		}
		return 90
	})
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: human, Events: []telemetry.Event{{Tick: 14, Type: telemetry.EvFire, WeaponID: "rifle"}}}))

	// a shot long after the snap is not a snap shot
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: snap, Events: []telemetry.Event{{Tick: 19, Type: telemetry.EvFire, WeaponID: "rifle"}}}))
}

func TestAimReaction(t *testing.T) {
	e := env(t)
	snap := aimTrace(20, func(i int) float64 {
		if i >= 10 {
			return 90
		}
		return 0
	})
	hit := telemetry.Event{Tick: 12, Type: telemetry.EvHit, WeaponID: "rifle", TargetID: "e1", TargetVisibleSince: 10,
		Origin: telemetry.Vec3{}, Impact: telemetry.Vec3{X: 100}} // the view at tick 12 is yaw 90 = +X: on target
	got := run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: snap, Events: []telemetry.Event{hit}})
	want(t, got, "aim.reaction")

	hit.TargetVisibleSince = 2 // the target had been visible for half a second: a late flick, not a reaction
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: snap, Events: []telemetry.Event{hit}}))
}

func TestAimRobotic(t *testing.T) {
	e := env(t)
	robot := aimTrace(30, func(i int) float64 { return 0.5 * float64(i) }) // a perfect lerp
	fire := []telemetry.Event{{Tick: 25, Type: telemetry.EvFire, WeaponID: "rifle"}}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Device: "mouse", Samples: robot, Events: fire}), "aim.robotic")
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Device: "gamepad", Samples: robot, Events: fire})) // a stick is smooth

	rng := rand.New(rand.NewSource(1))
	human := aimTrace(30, func(i int) float64 { return 0.5*float64(i) + rng.NormFloat64()*0.2 })
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Device: "mouse", Samples: human, Events: fire}))

	still := aimTrace(30, func(int) float64 { return 0 }) // not moving: zero jitter is legitimate
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Device: "mouse", Samples: still, Events: fire}))
}

func TestAimSilent(t *testing.T) {
	e := env(t)
	look := aimTrace(10, func(int) float64 { return 0 })                                                              // looking along +Z
	hit := telemetry.Event{Tick: 5, Type: telemetry.EvHit, WeaponID: "rifle", Impact: telemetry.Vec3{X: 100, Z: 100}} // 45 deg off
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: look, Events: []telemetry.Event{hit}}), "aim.silent")

	hit.Impact = telemetry.Vec3{X: 5, Z: 100} // 2.9 deg: inside the rifle's cone
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: look, Events: []telemetry.Event{hit}}))
}

func fires(weapon string, ticks ...uint64) []telemetry.Event {
	var ev []telemetry.Event
	for _, t := range ticks {
		ev = append(ev, telemetry.Event{Tick: t, Type: telemetry.EvFire, WeaponID: weapon})
	}
	return ev
}

func TestCombat(t *testing.T) {
	e := env(t)
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: fires("sniper", 10, 40, 70)}))                     // 0.67/s
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: fires("sniper", 10, 15, 20)}), "combat.fire_rate") // 4/s

	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: fires("railgun", 10, 11, 12)}), "combat.unknown_weapon")

	ev := []telemetry.Event{{Tick: 100, Type: telemetry.EvReloadStart, WeaponID: "sniper"}}
	ev = append(ev, fires("sniper", 120)...)                                                     // mid-reload
	ev = append(ev, telemetry.Event{Tick: 130, Type: telemetry.EvReloadEnd, WeaponID: "sniper"}) // 1500 of 3500 ms
	got := run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: ev})
	want(t, got, "combat.reload")
	if n := len(got["combat.reload"]); n != 2 {
		t.Errorf("the shot and the early end: %d findings", n)
	}

	ev = []telemetry.Event{{Tick: 100, Type: telemetry.EvReloadStart, WeaponID: "sniper"},
		{Tick: 110, Type: telemetry.EvWeaponSwitch, WeaponID: "sniper"}} // switching cancels the reload
	ev = append(ev, fires("sniper", 120)...)
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: ev}))

	ev = []telemetry.Event{{Tick: 100, Type: telemetry.EvReloadStart, WeaponID: "sniper"},
		{Tick: 170, Type: telemetry.EvReloadEnd, WeaponID: "sniper"}} // 5 rounds
	ev = append(ev, fires("sniper", 200, 230, 260, 290, 320, 350)...) // 6 shots
	got = run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: ev})
	want(t, got, "combat.magazine")
	if got["combat.magazine"][0].Tick != 350 {
		t.Errorf("the sixth shot is the empty one, got tick %d", got["combat.magazine"][0].Tick)
	}

	hit := telemetry.Event{Tick: 5, Type: telemetry.EvHit, WeaponID: "sniper", Impact: telemetry.Vec3{Z: 2500}, Damage: 120}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: []telemetry.Event{hit}}), "combat.range")

	hit = telemetry.Event{Tick: 5, Type: telemetry.EvHit, WeaponID: "sniper", Impact: telemetry.Vec3{Z: 500}, Damage: 300, Headshot: true}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: []telemetry.Event{hit}})) // 120 x 2.5
	hit.Damage = 400
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: []telemetry.Event{hit}}), "combat.damage")
	hit.Headshot, hit.Damage = false, 150
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Events: []telemetry.Event{hit}}), "combat.damage")
}

func TestVisionPreaim(t *testing.T) {
	e := env(t)
	track := func(n int, moving, visible bool) []telemetry.Sample {
		s := make([]telemetry.Sample, n)
		for i := range s {
			x := 0.0
			if moving {
				x = 2 * float64(i)
			}
			enemy := telemetry.Vec3{X: x, Z: 100}
			yaw := math.Atan2(enemy.X, enemy.Z) * 180 / math.Pi
			s[i] = telemetry.Sample{Tick: uint64(i + 1), Rot: telemetry.Rot{Yaw: yaw}, Flags: telemetry.FlagGrounded,
				Contacts: []telemetry.Contact{{ID: "e1", Pos: enemy, Visible: visible}}}
		}
		return s
	}
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: track(40, true, false)}), "vision.preaim")
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: track(40, true, true)}))   // visible: just aiming
	want(t, run(t, e, NewPlayerState("p1"), telemetry.Batch{Samples: track(40, false, false)})) // holding an angle
}

func TestReplayIsIgnored(t *testing.T) {
	e := env(t)
	st := NewPlayerState("p1")
	b := telemetry.Batch{Samples: walk(40, 100), Events: fires("sniper", 10, 15, 20)}
	want(t, run(t, e, st, b), "movement.speed", "combat.fire_rate")
	want(t, run(t, e, st, b)) // the same ticks again (a retransmit): nothing new
}
