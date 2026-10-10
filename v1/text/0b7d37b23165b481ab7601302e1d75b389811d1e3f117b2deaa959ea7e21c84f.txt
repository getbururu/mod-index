<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/readme-header-dark.svg">
  <img src=".github/readme-header-light.svg" alt="Bururu">
</picture>

# Elite Dangerous mod for Bururu

This mod brings Elite Dangerous to your DualSense through Bururu, the app that drives the controller from what happens in a game. Weapons, boosts, shield hits, heat, jumps, docking and landings play as haptics, and the adaptive triggers, the lightbar and the LEDs follow the game.

## What you feel

- **Haptics**: 97 feels, from the multi-cannon spin-up to the hyperspace swell. Each weapon type has its own feel, on the side of its trigger.
- **Triggers**: R2 and L2 change with the situation: hardpoints out, a weapon reloading, an empty weapons capacitor, overheating, analysis mode, an interdiction, a hit, the SRV turret, on foot.
- **Lightbar**: your hull (or your health on foot) from full to low, and colours for supercruise, hyperspace, the FSD charge, fuel scooping, interdictions, shields, hits and docking.
- **LEDs**: the fire group and the jump countdown on the player LEDs; low fuel and silent running on the mic LED.
- **Gyro aim**: it pauses in the main menu and in the panels and maps you pick.

## Install

In Bururu, open **Mods**, then **Browse**, and click **Install** on Elite Dangerous. Or download `elite-<version>.brr` from [Releases](https://github.com/getbururu/bururu-elite/releases) and use **Install from file...** on the **Mods** page.

## Set up Elite

- Run Elite **borderless or windowed**, so the mod can read the HUD. Exclusive fullscreen cannot be read.
- Use a **custom control preset**: change any binding in Elite once and it saves one. The heat sink, chaff, shield cell and boost feels need it. Bururu's **Controller** page checks it.
- If the ship drifts with the sticks at rest, set the dead zone of roll and pitch to about 0.08 in Elite's controls. Elite gets the sticks raw.
- Turn Steam Input off for Elite (Steam library -> Elite Dangerous -> **Properties** -> **Controller**).
- Start Elite after Bururu. Elite looks for controllers only as it starts.
- If Elite runs as administrator, Windows keeps gyro aim from it: start Elite and Steam normally.
- The pause menu (Esc) cannot be seen from outside the game, so gyro aim still moves the cursor there.

### On Bluetooth

With Bururu's Bluetooth drivers, Elite reads Bururu's virtual controller while it runs, on the cable too. Elite calls it `DualShock4`. A preset made for the DualSense, which Elite names `054C0CE6`, then does not load (`BindingLoadingErrors.log` says `Missing devices`).

To use one preset with and without the drivers, add these lines under `<DualShock4>` in `ControlSchemes\DeviceMappings.xml` (in Elite's `Products\elite-dangerous-odyssey-64` folder):

```xml
<Alternative><PID>0CE6</PID><VID>054C</VID></Alternative>
<Alternative><PID>0DF2</PID><VID>054C</VID></Alternative>
```

Then bind your controls again, or, with Elite closed, replace `054C0CE6` with `DualShock4` in your preset's `.binds` file. A game update can put the stock file back.

## What it reads

| What | What for |
|---|---|
| the journal and `Status.json`, in `Saved Games\Frontier Developments\Elite Dangerous` | what happens in the game |
| Elite's options and your control presets, in `%LOCALAPPDATA%\Frontier Developments\Elite Dangerous\Options` | your bindings, dead zones and HUD colours |
| the screen | shields, heat and weapon lists from the HUD |
| the keys you bound to Elite's actions, while Elite is in front | the heat sink, chaff, shield cell and boost feels |

It writes none of Elite's files and opens no network connection. Bururu's install dialog lists all of this.

## Its pages

The mod's pages are under **Elite Dangerous** in Bururu's sidebar.

- **Effects -> Haptics**: the turn feel (Waves, Push or Off) and the jump feel (Swell, Calm or Off); the weapon feel of each fire group; and under **Fine-tune**, the level of every feel, the multi-cannon spin-up per hardpoint size, and each feel on the rumble motors.
- **Effects -> Triggers**: the trigger effect of each situation, with **Try on R2**.
- **Effects -> Lights**: the lightbar colours.
- **Settings**: the HUD reader (on or off, its colours for recoloured HUDs, captures for a problem report), the panels and maps where gyro aim pauses, and the journal and bindings folders (empty means Elite's standard ones).

Bururu keeps what you change in `mod-settings\elite.json`, and only what differs from the defaults.

## The feels

Each feel is a file in [`feels/`](feels), `<name>.feel.json`.

| Group | Feels |
|---|---|
| Weapons | `weapon_multicannon`, `weapon_pulse`, `weapon_burst`, `weapon_beam`, `weapon_cannon`, `weapon_fragment`, `weapon_plasma`, `weapon_mining`, `weapon_other`, `spin_up`, `railgun_charge`, `rail_crack`, `missile_launch`, `reload`, `reload_done` |
| Flight | `thrust`, `thrust_low`, `boost`, `boost_empty`, `maneuver_waves`, `maneuver_push`, `maneuver_kick`, `fa_off`, `fa_on` |
| Ship systems | `hardpoints`, `landing_gear`, `cargo_scoop`, `silent_running`, `pips`, `fire_group`, `target_locked`, `heat_sink`, `chaff`, `shield_cell`, `ecm`, `scanner`, `utility`, `limpet` |
| Heat | `heat_build`, `heat_notch`, `heat_warning`, `heat_damage`, `overheat` |
| Travel | `fsd_charge`, `fsd_ready`, `hyperspace`, `supercruise_in`, `supercruise_out`, `mass_lock`, `mass_unlock`, `interdicted`, `interdiction`, `interdiction_noise`, `escaped`, `jet_cone`, `fuel_scoop`, `honk` |
| Docking and landing | `docking_granted`, `docked`, `undocked`, `touchdown`, `liftoff`, `vehicle`, `glide_start`, `glide`, `glide_low`, `glide_end`, `ground_rush` |
| Shields and hull | `shield_hit`, `shield_low`, `shield_regen`, `shields_down`, `shields_offline`, `shields_up`, `hull_hit`, `hull_hit_hud`, `hull_creak`, `hull_breach` |
| Combat | `under_attack`, `scanned`, `target_hit`, `target_hull_hit`, `target_shield_break`, `kill`, `died` |
| Cargo and messages | `cargo_collect`, `cargo_eject`, `message` |
| Thargoids | `thargoid`, `thargoid_pulse`, `thargoid_combat`, `thargoid_pulse_combat`, `systems_shutdown`, `systems_reboot` |
| On foot | `shot_kinetic`, `shot_laser`, `shot_plasma` |

## Change it with an add-on

For yourself, **Fine-tune** has a level for each feel, and 0% turns one off. To change how a feel plays, or what plays when, make an add-on: it changes this mod by name without copying it, so it keeps working when this mod updates. In Bururu's folder:

```
.\brr mod new quiet-cockpit --template addon
```

In its `manifest.json`, put `elite` in `for` and `dependencies`, with a version range that holds this mod's version:

```jsonc
"kind": "addon",
"for": ["elite"],
"dependencies": { "elite": { "kind": "required", "version": ">=0.1.0 <1.0.0" } }
```

Put its changes under `overrides/elite/`. This one, `overrides/elite/feels/shield_hit.feel.patch.json`, makes shield hits softer (the voice ids are in the feel's file here):

```jsonc
{ "voices": { "v1": { "level": 0.2 } } }
```

Then check it, with the list of what it changes:

```
.\brr mod check mods\quiet-cockpit --conflicts
```

An add-on can also change rules, light rows, setting defaults and pages, and run Lua at this mod's hook points:

| Hook point | What a handler may change |
|---|---|
| `weapon_layer@1` | before each weapon layer: its feel, level and side, or skip it |
| `turn@1` | before the turn feel: its level, mode and kick |
| `heat@1` | before the heat feel: its level |
| `boost@1` | before a boost plays: the feel, or skip it |
| `state@1` | after each journal or status update: read only |

Bururu's modding guide, in the `docs\modding` folder next to `Bururu.exe`, explains add-ons in `quickstart-addon.md` and `sharing.md`.

## Working on this mod

- Clone it into Bururu's `mods` folder, in a folder named after the mod's id: `git clone https://github.com/getbururu/bururu-elite mods\elite`. Bururu skips `.git` and `.github`, so the clone loads as it is.
- `feels.RPP` is the REAPER project of the feels (see Bururu's REAPER kit, `mods\templates\reaper`). After a change there, `.\brr feel import feels.RPP --mod mods\elite` writes the feel files again. Packs leave the project out.
- `tests/` holds recordings of play with what the mod does in them: `.\brr mod replay mods\elite mods\elite\tests\combat.replay.jsonl` plays one and compares.
- A `v<version>` tag runs [the release workflow](.github/workflows/release.yml), which packs the mod and publishes `elite-<version>.brr` with `SHA256SUMS`. The tag must match `version` in `manifest.json`, and `CHANGELOG.md` needs a `## <version>` section.

## Licence

MIT, see [LICENSE](LICENSE). Bururu is not made or endorsed by Frontier Developments. Elite Dangerous is a trademark of Frontier Developments plc.
