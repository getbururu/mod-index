<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/readme-header-dark.svg">
  <img src=".github/readme-header-light.svg" alt="Bururu">
</picture>

# Valheim mod for Bururu

This mod brings Valheim to your DualSense through Bururu, the app that drives the controller from what happens in a game. Your attacks, the bow and your block play as haptics, R2 and L2 get trigger effects, and raids and nights in bed come from Valheim's log.

## What you feel

- **Attacks**: pick Swing or Bow on the mod's page. Swing plays a soft swell each time you attack. Bow follows the attack button as a draw: a click as you start, a tick when the bow is fully drawn, and the string's snap when you let go, weaker after a short draw.
- **Block**: a light thud as you raise your block.
- **Triggers**: R2 gives a light click with Swing, and gets heavier the further you pull with Bow. L2 resists a little, for the block.
- **Raids and sleep**: drums as a raid starts, and one slow swell as the night passes in bed.
- **Lightbar**: a dim fire colour while you play, blinking red for a few seconds as a raid starts.

## What it cannot feel yet

Valheim writes none of these to its log, and Bururu cannot read Valheim's health and stamina bars from the screen yet:

- hits you take, low health and death
- stamina running out, eating, the rested bonus
- a parry, the hits you land, chopping and mining hits
- sailing and the wind, rain and thunder

The mod can add them once Bururu reads those bars.

## Install

In Bururu, open **Mods**, then **Browse**, and click **Install** on Valheim. Or download `valheim-<version>.brr` from [Releases](https://github.com/getbururu/mod-valheim/releases) and use **Install from file...** on the **Mods** page.

## Set up

- On the mod's **Settings** page, set **Attack button** to the button Valheim attacks with. In Valheim's Classic gamepad layout that is R2. Valheim's gamepad settings show what each layout puts on the buttons; one of the newer layouts attacks with R1 and blocks with L1. With R1, the triggers get no effects.
- Bururu cannot see what you hold, so it cannot tell a bow from an axe. Set **Attack feel** to Bow while you use the bow, and back to Swing for melee and tools. **Bow draw time** sets when the full-draw tick comes.
- Since 1.0, Valheim rumbles DualShock and Xbox controllers itself, so its rumble can come on top of Bururu's feels. If they get in each other's way, look for a vibration option in Valheim's controller settings.
- The night swell only comes on the PC where the world runs: when you play alone or host. Raids reach every player's log, so with friends you also hear the drums for a raid at someone else's base.
- Valheim's inventory, map and pause menu cannot be seen from outside the game, so the attack and block feels still play there.

## What it reads

| What | What for |
|---|---|
| `Player.log`, in `%USERPROFILE%\AppData\LocalLow\IronGate\Valheim` (Linux: `~/.config/unity3d/IronGate/Valheim`) | whether you are in a world or the main menu, raids, nights in bed |
| your controller's buttons, like every mod | the attack, bow and block feels |

It writes none of Valheim's files and opens no network connection. Bururu's install dialog lists all of this.

## Its pages

The mod's pages are under **Valheim** in Bururu's sidebar.

- **Effects -> Haptics**: the attack feel (Swing, Bow or Off) and the bow draw time; under **Fine-tune**, the level of every feel and each feel on the rumble motors.
- **Effects -> Triggers**: the trigger effect of each situation, with **Try on R2**.
- **Effects -> Lights**: the lightbar colours.
- **Settings**: the attack button, and the log folder (empty means Valheim's standard one).

Bururu keeps what you change in `mod-settings\valheim.json`, and only what differs from the defaults.

## The feels

Each feel is a file in [`feels/`](feels), `<name>.feel.json`.

| Group | Feels |
|---|---|
| Fighting | `swing`, `bow_nock`, `bow_full`, `bow_release`, `block_raise` |
| The world | `raid`, `rested` |

## Change it with an add-on

For yourself, **Fine-tune** has a level for each feel, and 0% turns one off. To change how a feel plays, or what plays when, make an add-on: it changes this mod by name without copying it, so it keeps working when this mod updates. In Bururu's folder:

```
.\brr mod new quiet-viking --template addon
```

In its `manifest.json`, put `valheim` in `for` and `dependencies`, with a version range that holds this mod's version:

```jsonc
"kind": "addon",
"for": ["valheim"],
"dependencies": { "valheim": { "kind": "required", "version": ">=0.1.0 <1.0.0" } }
```

Put its changes under `overrides/valheim/`. This one, `overrides/valheim/feels/bow_release.feel.patch.json`, makes the string's snap softer (the voice ids are in the feel's file here):

```jsonc
{ "voices": { "snap": { "level": 0.4 } } }
```

An add-on can also add line rules to the log sensor (`lines` is patchable), change rules, light rows, setting defaults and pages, and run Lua at this mod's hook points:

| Hook point | What a handler may change |
|---|---|
| `attack@1` | before an attack feel plays: the feel, its level, or skip it |
| `state@1` | after each log line the mod reads: read only |

Bururu's modding guide, in the `docs\modding` folder next to `Bururu.exe`, explains add-ons in `quickstart-addon.md` and `sharing.md`.

## Working on this mod

- Clone it into Bururu's `mods` folder, in a folder named after the mod's id: `git clone https://github.com/getbururu/mod-valheim mods\valheim`. Bururu skips `.git` and `.github`, so the clone loads as it is.
- The feels are written by hand. `.\brr feel render mods\valheim\feels\swing.feel.json --out swing.wav` writes one as a sound file.
- `tests/` holds made-up recordings with what the mod does in them: `.\brr mod replay mods\valheim mods\valheim\tests\session.replay.jsonl` plays one and compares. They run with the default settings, so they cover Swing. `tests/player-log.sample.txt` is a sample of `Player.log` with the lines the mod looks for.
- A `v<version>` tag runs [the release workflow](.github/workflows/release.yml), which packs the mod and publishes `valheim-<version>.brr` with `SHA256SUMS`. The tag must match `version` in `manifest.json`, and `CHANGELOG.md` needs a `## <version>` section.

## Licence

MIT, see [LICENSE](LICENSE). Bururu is not made or endorsed by Iron Gate or Coffee Stain. Valheim is made by Iron Gate and published by Coffee Stain.
