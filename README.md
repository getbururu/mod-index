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
| `games/<game-id>.json` | the games mods are made for: store ids, program names and marker files |
| `policy/` | names nobody else may take, versions taken back, Bururu versions with a known flaw, the oldest Bururu the list asks for |
| `files/icons/`, `files/text/` | the icons, READMEs and CHANGELOGs the list points at, named by their SHA-256 |
| `tools/indexer/` | the Go program the workflows run |
| `tools/bururu.ref` | the branch, tag or commit of Bururu whose `mod check` and `mod listing` the poll runs |

## How it runs

| Workflow | When | What |
|---|---|---|
| `check-entry` | every pull request | reads every data file, runs the tests, and looks at the repo and newest release of each new or changed entry |
| `check-report` | after `check-entry` | posts the report on the pull request |
| `poll` | every 6 hours, and when an entry changes | checks each new release with Bururu and opens one pull request per version; a maintainer's merge is the review |
| `publish` | when `state/`, `games/`, `policy/` or the indexer change | builds `index.json` and puts it on the site |
| `stats` | daily | download counts and GitHub stars, as `stats.json` beside the list |

Bururu's repo is private, so `poll` reads it with the `BURURU_READ`
secret, a read-only token for `getbururu/bururu`. Without it, nothing is
polled.

## Licence

MIT, see [LICENSE](LICENSE). Each listed mod has its own licence, named
in its repo.
