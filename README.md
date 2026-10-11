<picture>
  <source media="(prefers-color-scheme: dark)" srcset=".github/readme-header-dark.svg">
  <img src=".github/readme-header-light.svg" alt="Bururu">
</picture>

# Bururu's mod list

This is the list of mods you see in Bururu under Mods, Browse.

Each mod lives in its author's own GitHub repo and ships as a release
asset named `<id>-<version>.brr`. This repo only holds one small file per
mod that says where the mod lives. A GitHub Action checks each new
release, a maintainer reviews it, and the list is published at
`https://getbururu.github.io/mod-index/v1/index.json`.

Bururu downloads that file over HTTPS. For every version it lists the
SHA-256 of the `.brr`, so Bururu refuses a download that is not the file
that was reviewed. Bururu sends nothing about you, and installs a mod
only when you click Install.

- To list your mod: [docs/listing.md](docs/listing.md).
- To report a mod: [open a report](https://github.com/getbururu/mod-index/issues/new?template=report-a-mod.yml).
  If a mod does harm, a maintainer can switch it off in every Bururu.

## What is here

| Path | What |
|---|---|
| `entries/<id>.json` | one per listed mod, added by its author's pull request |
| `state/<id>.json` | the accepted versions of each mod with their hashes; written by the bot's pull requests only |
| `games/<game-id>.json` | the games Bururu knows: store ids, program names, marker files and the game's controller profile, see [Game profiles](#game-profiles) |
| `policy/` | names nobody else may take, versions taken back, Bururu versions with a known flaw, the oldest Bururu the list asks for |
| `files/icons/`, `files/text/` | the icons, READMEs and CHANGELOGs the list points at, named by their SHA-256 |
| `tools/indexer/` | the Go program the workflows run |
| `tools/bururu.json` | the published Bururu build whose `mod check` and `mod listing` the poll runs, see [The Bururu the poll runs](#the-bururu-the-poll-runs) |

## How it runs

| Workflow | When | What |
|---|---|---|
| `check-entry` | every pull request | reads every data file, runs the tests, looks at the repo and newest release of each new or changed entry, and checks a change of `tools/bururu.json` |
| `check-report` | after `check-entry` | posts the report on the pull request |
| `poll` | every 6 hours, and when an entry or `tools/bururu.json` changes | checks each new release with the pinned Bururu build and opens one pull request per version; a maintainer's merge is the review |
| `publish` | when `state/`, `games/`, `policy/` or the indexer change | builds `index.json` and puts it on the site |
| `stats` | daily | download counts and GitHub stars, as `stats.json` beside the list |

No workflow here uses a secret or reads Bururu's code, which is closed
source. `poll` downloads a published Bururu build instead, the one
`tools/bururu.json` pins. Until a build is pinned, the poll says so and
polls nothing, and maintainers poll by hand.

## The Bururu the poll runs

`tools/bururu.json` pins one published Bururu build for Linux:

```json
{
  "bururu": 1,
  "version": "1.0.0",
  "url": "https://<host>/<path of the 1.0.0 release>/Bururu-x86_64.AppImage",
  "sha256": "<the SHA-256 of that file>",
  "path": "usr/bin/bururu"
}
```

| Field | What |
|---|---|
| `bururu` | always 1 |
| `version` | Bururu's version in that build; the checks of each version name it |
| `url` | the `https` address of the build: an `.AppImage` (Bururu's Linux release is `Bururu-x86_64.AppImage`) or a `.tar.gz` |
| `sha256` | the SHA-256 of that file, as the release's `SHA256SUMS.txt` lists it |
| `path` | the `bururu` program inside the build: `usr/bin/bururu` in the AppImage |

With every field but `bururu` empty, no build is pinned. That is the case
until Bururu has a public release.

The indexer downloads the file, refuses it unless its SHA-256 is the
pinned one, takes the program out of it, and runs `bururu -version`,
which must print the pinned version. It unpacks an AppImage by running
it with `--appimage-extract`, which needs no FUSE on GitHub's runner,
and reads a `.tar.gz` itself. In the AppImage, `usr/bin/bururu` is
Bururu's core (`AppRun` points at it). The core is a static program,
and its `mod check` and `mod listing` need no display and no GTK. A
plain tarball of the core would work as well, if Bururu ever ships one.

To pin a Bururu version, fill in all four fields of `tools/bururu.json`
and open a pull request:

- `version`: the version of that release, such as `1.0.0`;
- `url`: the address of the file in that version's own release, not a
  link to the latest release, whose file changes;
- `sha256`: the file's SHA-256 from that release's `SHA256SUMS.txt`;
- `path`: `usr/bin/bururu` for the AppImage.

A pin with some fields empty does not read. Only the list's maintainers
change this file: `check-entry` fails anyone else's change, and its
report shows the pin before and after. For a maintainer's pull request
it also downloads the file and checks its SHA-256, so a wrong address or
hash shows before the merge. It does not unpack or run the build; the
poll does that after the merge. To try the whole thing first, run the
poll on the branch on Linux x86_64 (WSL works) without `-bururu`:

```
go run ./tools/indexer poll -out out -only <id>
```

It downloads the build, checks its SHA-256, unpacks it, checks that
`bururu -version` prints the pinned version and polls that one mod. Run
it somewhere with nothing to lose, as the next section says.

## Polling by hand

Until a build is pinned, or to try a test build of Bururu, a maintainer
runs the poll with a Bururu program of their own and gives the commit it
was built from with `-ref` (the checks of each version record it):

```
go run ./tools/indexer poll -bururu <test build folder>\brr.exe -ref <commit> -out out -only <id>
go run ./tools/indexer plan -in out
go run ./tools/indexer apply -in out -key <key> -body out\body.md
```

`plan` prints one line per change: its key, the mod's id, the bot's
branch name and the title. `apply` writes that change into `state/` and
`files/`, and the pull request's text into `out\body.md`. Commit
`state/` and `files/` on a new branch and open a pull request with that
title and text; merging it is the review, as with the bot's pull
requests.

The poll runs `mod check` on packs nobody has reviewed yet, and that
runs each pack's Lua. So poll by hand somewhere with nothing to lose: a
VM, Windows Sandbox, or a WSL distro with a Linux build of Bururu, not
the desktop you work on. The indexer starts Bururu without the
environment variables that may hold a secret (`GITHUB_TOKEN`,
`GH_TOKEN`, `*_SECRET`, `*_PASSWORD` and the like). The poll needs no
token. If GitHub's limit for requests without one gets in the way, set
`GITHUB_TOKEN` to a fine-grained token that can only read public repos.
Do not use what `gh auth token` prints: that token can write to every
repo you can.

## Game profiles

A game's record may say which controller Bururu shows the game, once we
have tested it:

```json
"controller": { "cable": "direct", "bluetooth": "dualsense", "steam_input": "off" }
```

| Key | What |
|---|---|
| `cable` | what the game sees while the DualSense is on its cable |
| `bluetooth` | the same while it is on Bluetooth |
| `steam_input` | `off` when the game works only with Steam Input off for it |

The controllers are `direct` (the game reads the DualSense itself),
`dualsense`, `dualshock4` and `xbox360` (one of Bururu's virtual
controllers). Leave out a key we have not tested. This is what Bururu
picks on Automatic; the player can pick another controller for the game
in Bururu, and their choice stays.

Bururu skips a key it does not know in `controller`, and takes a value
it does not know as no profile for that connection, so a new key or
controller does not break older Bururus. The check here takes only the
keys and values above.

## Licence

MIT, see [LICENSE](LICENSE). Each listed mod has its own licence, named
in its repo.
