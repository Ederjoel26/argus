// Package telemetry is the wire format between a game server and the anti-cheat engine.
//
// Trust boundary: a Batch is what the *game server* observed about a player (positions it relayed, shots and hits
// it received, visibility it computed), stamped with the server's own tick. Nothing in it is produced by the
// anti-cheat client side; the client is the thing under test. proto/telemetry.proto is the same schema for gRPC.
package telemetry

import (
	"errors"
	"math"
	"sort"
)

// Vec3 is a position or direction in world units. Y is up.
type Vec3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

func (a Vec3) Sub(b Vec3) Vec3     { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Dot(b Vec3) float64  { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) Len() float64        { return math.Sqrt(a.Dot(a)) }
func (a Vec3) Horizontal() float64 { return math.Hypot(a.X, a.Z) }

// Norm is the unit vector of a, or the zero vector for a zero vector.
func (a Vec3) Norm() Vec3 {
	l := a.Len()
	if l == 0 {
		return Vec3{}
	}
	return Vec3{a.X / l, a.Y / l, a.Z / l}
}

// AngleDeg is the angle between two directions in degrees, robust to yaw wrap-around and to float noise.
func AngleDeg(a, b Vec3) float64 {
	d := a.Norm().Dot(b.Norm())
	return math.Acos(math.Max(-1, math.Min(1, d))) * 180 / math.Pi
}

// Rot is a view direction: yaw around Y (0 = +Z), pitch up positive, both in degrees.
type Rot struct {
	Yaw   float64 `json:"yaw"`
	Pitch float64 `json:"pitch"`
}

// Forward is the unit vector the camera looks along.
func (r Rot) Forward() Vec3 {
	yaw, pitch := r.Yaw*math.Pi/180, r.Pitch*math.Pi/180
	c := math.Cos(pitch)
	return Vec3{X: math.Sin(yaw) * c, Y: math.Sin(pitch), Z: math.Cos(yaw) * c}
}

// Input bits: what the player was pressing during the sample's tick.
const (
	InForward uint32 = 1 << iota
	InBack
	InLeft
	InRight
	InJump
	InCrouch
	InSprint
	InFire
	InADS
	InReload
)

// Flag bits: the player's state as the game server knows it. A set Teleport bit tells the movement check that a
// displacement is authorized (spawn, respawn, server-side knockback, a taxi drop) and resets its history.
const (
	FlagGrounded uint32 = 1 << iota
	FlagInVehicle
	FlagGliding
	FlagSwimming
	FlagDead
	FlagSpectating
	FlagTeleport
)

// Contact is an enemy near the player at a sample's tick, with the server's own occlusion result. Optional; the
// wallhack heuristics need it.
type Contact struct {
	ID      string `json:"id"`
	Pos     Vec3   `json:"position"`
	Visible bool   `json:"visible"`
}

// Sample is one tick of a player's movement and view.
type Sample struct {
	Tick     uint64    `json:"tick"`
	Pos      Vec3      `json:"position"`
	Rot      Rot       `json:"rotation"`
	Inputs   uint32    `json:"inputs"`
	Flags    uint32    `json:"flags"`
	Contacts []Contact `json:"contacts,omitempty"`
}

// EventType names a discrete action. Fire is one trigger pull (a shotgun's pellets are one Fire and several Hits).
type EventType string

const (
	EvFire         EventType = "fire"
	EvHit          EventType = "hit"
	EvReloadStart  EventType = "reload_start"
	EvReloadEnd    EventType = "reload_end"
	EvWeaponSwitch EventType = "weapon_switch"
)

// Event is a combat action at a tick. For a Hit, Origin and Impact are the server's own shot origin and point of
// impact (never the client's claimed distance), TargetVisibleSince is the tick the server first saw the target
// visible to the shooter in the current encounter (0: unknown).
type Event struct {
	Tick               uint64    `json:"tick"`
	Type               EventType `json:"type"`
	WeaponID           string    `json:"weapon_id,omitempty"`
	TargetID           string    `json:"target_id,omitempty"`
	Origin             Vec3      `json:"origin"`
	Impact             Vec3      `json:"impact"`
	Damage             float64   `json:"damage,omitempty"`
	Headshot           bool      `json:"headshot,omitempty"`
	TargetVisibleSince uint64    `json:"target_visible_since,omitempty"`
}

// Batch is a player's telemetry for a span of ticks, as the game server emits it (every N ticks, or at the end
// of a match).
type Batch struct {
	MatchID  string   `json:"match_id"`
	PlayerID string   `json:"player_id"`
	Device   string   `json:"device,omitempty"` // "mouse", "gamepad", "touch": some aim statistics only make sense for a mouse
	Samples  []Sample `json:"samples"`
	Events   []Event  `json:"events"`
}

// MaxBatchItems bounds what one batch may carry; a larger one is refused at the trust boundary.
const MaxBatchItems = 4096

var (
	ErrNoPlayer = errors.New("telemetry: batch without player_id")
	ErrTooBig   = errors.New("telemetry: batch too big")
)

// Validate refuses what the heuristics must not see: anonymous or oversized batches. It also sorts samples and
// events by tick, so the checks can walk them in order whatever the transport did.
func (b *Batch) Validate() error {
	if b.PlayerID == "" {
		return ErrNoPlayer
	}
	if len(b.Samples)+len(b.Events) > MaxBatchItems {
		return ErrTooBig
	}
	sort.SliceStable(b.Samples, func(i, j int) bool { return b.Samples[i].Tick < b.Samples[j].Tick })
	sort.SliceStable(b.Events, func(i, j int) bool { return b.Events[i].Tick < b.Events[j].Tick })
	return nil
}

// SampleAt is the sample of the tick, or the last one before it (the view a shot was taken with), or nil.
func (b *Batch) SampleAt(tick uint64) *Sample {
	i := sort.Search(len(b.Samples), func(i int) bool { return b.Samples[i].Tick > tick })
	if i == 0 {
		return nil
	}
	return &b.Samples[i-1]
}
