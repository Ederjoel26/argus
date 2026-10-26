# LinkedIn release post

> Fill in the repo link; the bracketed line about the first integration is optional.

---

Cheaters killed more small multiplayer games than bad netcode ever did.

A revived or indie shooter can't ship a kernel driver, can't afford an anti-cheat vendor, and still gets the same
aimbots and speed hacks as a AAA title. But it has the one vantage point no cheat can patch: the server.

So I built **argus**, an open-source, server-authoritative anti-cheat engine, and I'm releasing it today.

How it works: the game server streams what it *observed* about each player (tick-stamped positions, view angles,
shots, hits, occlusion) and argus validates that stream against the game's own physics and weapon rules, in near
real time:

🏃 Kinematics: velocity, acceleration, teleports, fly hacks, no-clip, from tick deltas, not client clocks
🎯 Aim telemetry: snap-and-stop flicks, sub-human reaction windows, jitter-free "robotic" tracking, silent aim
🔫 Combat rules: fire rate per weapon, reload and magazine state machines, range and damage caps
🧱 Wallhacks: prevented by occlusion-based interest management; tracking through walls caught statistically
📈 A decaying anomaly score with confidence, mapped to warning → shadowban → kick → disconnect

Any game plugs in with one config file: tick rate, speeds, weapon profiles, penalty thresholds. Nothing runs on the
player's machine.

The reference implementation is Go, standard library only, sharded by player so every state machine stays ordered
and lock-free. Every heuristic is a pure function with a synthetic-trace test next to it, so the math is auditable
and the thresholds are tunable in the open.

[First integration: the Creative Reborn community server, where hits were trusted as the client sent them.]

It's a start, not a silver bullet: the roadmap has gRPC/UDP ingest, Kafka/Redis sharding, a replay tool for
calibration and learned baselines per weapon. If you run a game and want to add a weapon profile, or you can build
a trace that fools a check, the repo is open.

🔗 github.com/Ederjoel26/argus

#gamedev #anticheat #opensource #golang #multiplayer #distributedsystems #gamesecurity
