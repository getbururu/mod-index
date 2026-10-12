<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/readme-header-dark.svg">
  <img src=".github/readme-header-light.svg" alt="Bururu">
</picture>

# ARC Raiders mod for Bururu

ARC Raiders on PC plays its own haptics on a DualSense connected by USB cable. As far as players can tell, it gives the adaptive triggers no effects there and leaves the lightbar alone. This mod adds those two through Bururu: a click on R2 as you fire, a light wall on L2 as you aim, and a lightbar colour. The haptics stay the game's own. For now the mod works on the cable only.

## What you feel

- **Triggers**: R2 clicks like a gun trigger, L2 has a light wall. Both are free while the game is in the background. The mod cannot tell a raid from the menus, so R2 also clicks in menus and when you throw or deploy something.
- **Lightbar**: a calm amber while the game runs, or off.
- **Haptics**: from the game itself, on the cable.

## Install

In Bururu, open **Mods**, then **Browse**, and click **Install** on ARC Raiders. Or download `arcraiders-<version>.brr` from [Releases](https://github.com/getbururu/mod-arcraiders/releases) and use **Install from file...** on the **Mods** page.

## Set up ARC Raiders

- Turn Steam Input off for ARC Raiders (Steam library -> ARC Raiders -> **Properties** -> **Controller**). With it on, the game can get every button twice.
- Connect the controller and start Bururu before you start ARC Raiders. Some players get haptics only when the controller was plugged in before the game started.
- On the cable, the game finds the DualSense by itself. If you feel no haptics, check that **Speakers (DualSense Wireless Controller)** is not disabled in Windows' sound settings: the game plays its haptics through it.
- On Bluetooth the game plays no haptics, and Bururu leaves the controller to the game while it runs, so the mod does nothing there. Bururu's virtual DualSense could bring both to Bluetooth, but that puts Bururu's drivers in front of the game's kernel-level anti-cheat, and we have not tried it yet.

## What it reads

Only the controller, as Bururu reads it for every game. It reads none of the game's files, not the screen and not the keyboard. It writes nothing and opens no network connection.

ARC Raiders uses kernel-level anti-cheat. Bururu never opens the game's process or reads its memory.

## What it cannot do yet

The mod does not know when a shot really goes off, when you reload, when the magazine is empty, or when you take a hit. We found no file where ARC Raiders writes these, and Bururu's screen reader cannot read its health and shield bars yet, so the triggers stay the same through a whole raid. The game's own haptics cover shots and hits.

## Its page

The mod's page is under **ARC Raiders** in Bururu's sidebar.

- **Effects -> Triggers**: the R2 and L2 effects, with **Try on R2**. Pick **Free** to turn one off.
- **Effects -> Lightbar**: on or off, and its colour.

Bururu keeps what you change in `mod-settings\arcraiders.json`, and only what differs from the defaults.

## Change it with an add-on

To change the effects for yourself, use the page. To change what the mod does, make an add-on: it changes this mod by name without copying it. In Bururu's folder:

```
.\brr mod new my-arc-triggers --template addon
```

In its `manifest.json`, put `arcraiders` in `for` and `dependencies`, with a version range that holds this mod's version:

```jsonc
"kind": "addon",
"for": ["arcraiders"],
"dependencies": { "arcraiders": { "kind": "required", "version": ">=0.1.0 <1.0.0" } }
```

The rows an add-on can patch in `overrides/arcraiders/lights.patch.json` are `weapons` (the triggers), `raider` and `dark` (the lightbar). Setting defaults go in `overrides/arcraiders/settings.defaults.json`.

Bururu's modding guide, in the `docs\modding` folder next to `Bururu.exe`, explains add-ons in `quickstart-addon.md` and `sharing.md`.

## Working on this mod

- Clone it into Bururu's `mods` folder, in a folder named after the mod's id: `git clone https://github.com/getbururu/mod-arcraiders mods\arcraiders`. Bururu skips `.git` and `.github`, so the clone loads as it is.
- `manifest.json` names the game and asks for no permissions, `lights.json` sets the triggers and the lightbar while the game runs, and `settings.schema.json` holds the effects and the page. `demo/demo.replay.jsonl` is what **Play demo** plays.
- `.\brr mod check mods\arcraiders` checks the mod.
- `tests/` holds a made-up session (aiming, three shots, alt-tab, closing the game) with what the mod does in it: `.\brr mod replay mods\arcraiders mods\arcraiders\tests\raid.replay.jsonl` plays it and compares. After a change you want, run it again with `--update`.
- A `v<version>` tag runs [the release workflow](.github/workflows/release.yml), which packs the mod and publishes `arcraiders-<version>.brr` with `SHA256SUMS`. The tag must match `version` in `manifest.json`, and `CHANGELOG.md` needs a `## <version>` section.

## Licence

MIT, see [LICENSE](LICENSE). Bururu is not made or endorsed by Embark Studios. ARC Raiders is a trademark of Embark Studios AB.
