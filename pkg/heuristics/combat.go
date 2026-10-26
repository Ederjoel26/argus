package heuristics

import (
	"math"

	"github.com/Ederjoel26/argus/pkg/telemetry"
)

// Combat is the action-rules check, a state machine per weapon the player holds:
//
//	unknown_weapon: a weapon_id not in weapon_profiles (an item the client invented); once per weapon.
//	fire_rate:      ticks since the last shot < ceil(tick_rate / fire_rate) - fire_rate_tolerance_ticks.
//	reload:         a shot between reload_start and reload_start + reload_ms, or a reload_end that early.
//	                A weapon_switch cancels a reload (switching cancels; firing cannot).
//	magazine:       a shot with 0 rounds left. The count is known from the first reload_end on.
//	range:          |impact - origin| > max_range (1+range_tolerance), both points the server's own.
//	damage:         damage > max_damage · headshot_multiplier (1+damage_tolerance).
type Combat struct{}

type weaponState struct {
	reported       bool
	hasFired       bool
	lastFire       uint64
	magazine       int // -1: unknown yet
	reloading      bool
	reloadStart    uint64
	reloadingUntil uint64
}

func (Combat) Name() string { return "combat" }

func (Combat) Apply(env *Env, st *PlayerState, b *telemetry.Batch) []Finding {
	cfg := env.Cfg
	var out []Finding
	for i := range b.Events {
		ev := &b.Events[i]
		if ev.WeaponID == "" {
			continue
		}
		ws := st.Weapons[ev.WeaponID]
		if ws == nil {
			ws = &weaponState{magazine: -1}
			st.Weapons[ev.WeaponID] = ws
		}
		w, ok := cfg.WeaponProfiles[ev.WeaponID]
		if !ok {
			if !ws.reported {
				ws.reported = true
				out = append(out, finding("combat.unknown_weapon", ev.Tick, 1, "weapon %q is not in the profile", ev.WeaponID))
			}
			continue
		}
		switch ev.Type {
		case telemetry.EvWeaponSwitch:
			ws.reloading = false
		case telemetry.EvReloadStart:
			ws.reloading, ws.reloadStart, ws.reloadingUntil = true, ev.Tick, ev.Tick+cfg.Ticks(w.ReloadMs)
		case telemetry.EvReloadEnd:
			if ws.reloading && ev.Tick+cfg.Combat.FireRateToleranceTicks < ws.reloadingUntil {
				ms := cfg.Dt(ws.reloadStart, ev.Tick) * 1000
				out = append(out, finding("combat.reload", ev.Tick, below(ms, w.ReloadMs), "reloaded in %.0f ms (takes %.0f)", ms, w.ReloadMs))
			}
			ws.reloading, ws.magazine = false, w.Magazine
		case telemetry.EvFire:
			if ws.reloading && ev.Tick < ws.reloadingUntil {
				out = append(out, finding("combat.reload", ev.Tick, 1, "fired %d ticks into a %.0f ms reload", ev.Tick-ws.reloadStart, w.ReloadMs))
			}
			minTicks := uint64(math.Ceil(cfg.TickRate / w.FireRate))
			if minTicks > cfg.Combat.FireRateToleranceTicks {
				minTicks -= cfg.Combat.FireRateToleranceTicks
			}
			if gap := ev.Tick - ws.lastFire; ws.hasFired && ev.Tick >= ws.lastFire && gap < minTicks {
				out = append(out, finding("combat.fire_rate", ev.Tick, below(float64(gap), float64(minTicks)),
					"%d ticks between shots (minimum %d for %.1f/s)", gap, minTicks, w.FireRate))
			}
			switch {
			case ws.magazine == 0:
				out = append(out, finding("combat.magazine", ev.Tick, 1, "fired with an empty %d-round magazine", w.Magazine))
			case ws.magazine > 0:
				ws.magazine--
			}
			ws.hasFired, ws.lastFire = true, ev.Tick
		case telemetry.EvHit:
			limit := w.MaxRange * (1 + cfg.Combat.RangeTolerance)
			if dist := ev.Impact.Sub(ev.Origin).Len(); dist > limit {
				out = append(out, finding("combat.range", ev.Tick, excess(dist, w.MaxRange), "hit at %.0f u (range %.0f)", dist, w.MaxRange))
			}
			maxDamage := w.MaxDamage
			if ev.Headshot && w.HeadshotMultiplier > 0 {
				maxDamage *= w.HeadshotMultiplier
			}
			if ev.Damage > maxDamage*(1+cfg.Combat.DamageTolerance) {
				out = append(out, finding("combat.damage", ev.Tick, excess(ev.Damage, maxDamage), "%.0f damage (max %.0f)", ev.Damage, maxDamage))
			}
		}
	}
	return out
}
