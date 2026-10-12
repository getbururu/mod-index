<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/readme-header-dark.svg">
  <img src=".github/readme-header-light.svg" alt="Bururu">
</picture>

# Star Citizen mod for Bururu

This mod brings Star Citizen to your DualSense through Bururu, the app that drives the controller from what happens in a game. It reads the game's `Game.log`, so you feel the pilot seat, quantum travel, jumps, hangars, injuries, the law and your contracts, and the adaptive triggers, the lightbar and the LEDs follow the game.

## What you feel

- **Haptics**: 31 feels, short taps and single swells, mostly around 150 to 200 Hz, where the controller feels smooth. From the controller itself: the turn feel, the boost and, if you want it, a light fire texture.
- **Triggers**: R2 and L2 resist in the pilot seat and on foot with something in your hand. They are free in armistice zones, while you are downed and in the main menu. A crash makes both buzz.
- **Lightbar**: your health (green, then amber, orange and red with your injuries) or the star system you are in. It dims in armistice zones, breathes in the jump tunnel and while you are downed, and flashes for arrivals, crimes, contracts and blueprints.
- **LEDs**: the player LEDs count the jump drive's steps, or show your place in a hangar queue. The mic LED is on outside monitored space, where crimes go unreported, and pulses after a Low Fuel notice.
- **Gyro aim**: it pauses in the main menu, and in the pilot seat if you turn it off there.

Star Citizen has not written combat to `Game.log` since 4.4: no hits, shields, kills or weapon fire. It never wrote speed, landing gear or touchdowns. So there are no hit or landing feels, and the triggers cannot tell whether your guns fire.

## Install

In Bururu, open **Mods**, then **Browse**, and click **Install** on Star Citizen. Or download `starcitizen-<version>.brr` from [Releases](https://github.com/getbururu/mod-starcitizen/releases) and use **Install from file...** on the **Mods** page.

## Set up Star Citizen

- Star Citizen reads one Xbox-style gamepad and has no DualSense support. On Automatic, Bururu shows it a virtual Xbox 360 controller, on the cable and on Bluetooth. If the Home card asks for Bururu's drivers, install them. If the game sees a joystick instead of a gamepad, pick the Xbox 360 controller for Star Citizen in Bururu.
- Start Bururu before the RSI Launcher. The virtual controller is plugged in when the launcher starts.
- The turn, boost and fire feels assume Star Citizen's default gamepad layout: the right stick pitches and yaws, L1 with the left stick rolls, L3 boosts, R2 and L2 fire weapon groups 1 and 2.
- The mod knows the game's notices in English. With a language pack, some moments (armistice zones, injuries, crimes, hangars) need an add-on, see below.
- The game's own menus (mobiGlas, the map) write nothing to the log, so gyro aim still moves the cursor there.

## What it reads

| What | What for |
|---|---|
| `Game.log` in the game's folder, such as `C:\Program Files\Roberts Space Industries\StarCitizen\LIVE` | what happens in the game |

Bururu finds the folder from the running game. If it does not, set the folder on the mod's **Settings** page. The mod writes none of the game's files and opens no network connection. Star Citizen uses Easy Anti-Cheat, so Bururu keeps its screen and keyboard readers off for this game. Bururu never reads a game's memory. Bururu's install dialog lists all of this.

## Its pages

The mod's pages are under **Star Citizen** in Bururu's sidebar.

- **Effects -> Haptics**: the turn feel (Push, Waves or Off), the quantum feel (Swell, Taps or Off), the jump feel (Swell, Calm or Off), the downed feel (Heartbeat or Off) and the fire feel (Off or Light); and under **Fine-tune**, the level of every feel and each feel on the rumble motors.
- **Effects -> Triggers**: the trigger effect of each situation, with **Try on R2**.
- **Effects -> Lights**: what the lightbar shows (health or star system) and its colours.
- **Settings**: gyro aim in the pilot seat, and the game's folder (empty means the folder of the running game).

The **Home** card shows your ship, the star system and your injury. Bururu keeps what you change in `mod-settings\starcitizen.json`, and only what differs from the defaults.

## The feels

Each feel is a file in [`feels/`](feels), `<name>.feel.json`.

| Group | Feels |
|---|---|
| Flying | `seat_in`, `seat_out`, `turn_push`, `turn_waves`, `boost`, `fire_light`, `crash`, `low_fuel` |
| Travel | `qt_target`, `qt_arrive`, `qt_arrive_taps`, `jump_step`, `jump_pulse`, `jump_swell` |
| Hangars | `hangar_ready`, `elevator`, `stowed` |
| Body | `injury`, `heartbeat`, `healed`, `died` |
| Law | `zone_in`, `zone_out`, `crime`, `attacked` |
| Missions and notices | `notice`, `success`, `failure`, `tick`, `sparkle`, `purchase` |

## Change it with an add-on

For yourself, **Fine-tune** has a level for each feel, and 0% turns one off. To change how a feel plays, or what plays when, make an add-on: it changes this mod by name without copying it, so it keeps working when this mod updates. In Bururu's folder:

```
.\brr mod new my-starcitizen --template addon
```

In its `manifest.json`, put `starcitizen` in `for` and `dependencies`, with a version range that holds this mod's version:

```jsonc
"kind": "addon",
"for": ["starcitizen"],
"dependencies": { "starcitizen": { "kind": "required", "version": ">=0.1.0 <1.0.0" } }
```

Put its changes under `overrides/starcitizen/`. For a language pack, patch the table of notices, `overrides/starcitizen/data/notifications.patch.json`, with the start of each notice in your language and the name the mod uses for it (the names are in [`data/notifications.json`](data/notifications.json)):

```jsonc
{ "prefix": { "<the notice in your language>": "armistice_in" } }
```

Then check it, with the list of what it changes:

```
.\brr mod check mods\my-starcitizen --conflicts
```

An add-on can also change feels, rules, light rows, setting defaults and pages, and run Lua at this mod's hook points:

| Hook point | What a handler may change |
|---|---|
| `turn@1` | before the turn feel: its mode, feel, level and swell |
| `state@1` | after each `Game.log` line the state reads: read only |

Bururu's modding guide, in the `docs\modding` folder next to `Bururu.exe`, explains add-ons in `quickstart-addon.md` and `sharing.md`.

## Known limits

- The line formats come from public log parsers and their sample lines. Some still need a check against a real 4.10 log:
  - Taking the pilot seat may not be logged in recent builds. Then the mod learns your ship when you pick a quantum target, and the pilot seat feel does not play.
  - The jump tunnel comes from the jump drive's own states (in [`data/states.json`](data/states.json)). Public logs show the steps before a jump, but not yet the tunnel.
  - Turret seats may write the same control token lines as the pilot seat.
  - Your death is logged mainly when your ship is destroyed, and some 2026 builds may not log it at all.
- Gyro aim reaches the game as mouse input. That has not been tried with Easy Anti-Cheat yet.
- The turn, boost and fire feels do not follow your own bindings yet. Reading them from `actionmaps.xml` is planned.

## Working on this mod

- Clone it into Bururu's `mods` folder, in a folder named after the mod's id: `git clone https://github.com/getbururu/mod-starcitizen mods\starcitizen`. Bururu skips `.git` and `.github`, so the clone loads as it is.
- `feels.RPP` is the REAPER project of the feels (see Bururu's REAPER kit, `mods\templates\reaper`). After a change there, `.\brr feel import feels.RPP --mod mods\starcitizen` writes the feel files again. Packs leave the project out.
- `tests/logs` holds made-up `Game.log` excerpts, one per test. `go run .github/logreplay/main.go -out tests/body.replay.jsonl tests/logs/body.log.txt` turns one into a recording, and `.\brr mod replay mods\starcitizen mods\starcitizen\tests\body.replay.jsonl` plays it through the mod and compares it with `tests/body.expect.txt`. The demos in `demo/` come from `demo/logs` the same way.
- The same tool turns a real log from the game's `logbackups` folder into a recording, so you can check the mod against your own play. Such a recording holds your handle and other players' names: keep it to yourself.
- A `v<version>` tag runs [the release workflow](.github/workflows/release.yml), which packs the mod and publishes `starcitizen-<version>.brr` with `SHA256SUMS`. The tag must match `version` in `manifest.json`, and `CHANGELOG.md` needs a `## <version>` section.

## Licence

MIT, see [LICENSE](LICENSE). Bururu is not made or endorsed by Cloud Imperium Games. Star Citizen is a trademark of Cloud Imperium Rights LLC.
