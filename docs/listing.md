# Listing a mod

A listed mod shows up in Bururu under Mods, Browse, with its icon, name,
author, download count and GitHub stars. A player clicks Install, and
Bururu downloads your release and checks it against the SHA-256 the list
recorded. Updates are offered, never installed by themselves.

You need three things: your mod in its own repo, a release with the
packed mod, and a pull request here that adds one small file.

## 1. Your repo

- A public GitHub repo you own, with the mod's files at its root
  (`manifest.json` and the rest), as `brr mod new` makes them.
- In `manifest.json`:
  - `id`: lower-case letters, digits and `-`, 2 to 40 characters,
    starting with a letter. It never changes once listed.
  - `name` and each of `authors`: plain ASCII (letters, digits, spaces,
    the usual punctuation), at most 64 characters.
  - `summary`: one line, at most 200 bytes, that says what the mod does.
  - `version`: like `1.2.0`, or `1.3.0-beta.1` for a test version.
  - `license`: the licence's name, with the licence file beside it.
- `README.md`: what the mod does and what it reads. Name every folder it
  reads, and say why it uses the screen or a UDP port if it does. Players
  see it on the mod's page in Bururu.
- `CHANGELOG.md`: a `## <version>` heading per version, newest first. The
  section of a version is what the update dialog shows.
- `icon.png` (optional): a square PNG, 64 to 256 pixels, at most 32 KB.

## 2. A release

1. Set `version` in `manifest.json` and add its section to `CHANGELOG.md`.
2. Check and pack the mod:

   ```
   .\brr mod check <your mod folder>
   .\brr mod pack <your mod folder> --out dist
   .\brr mod listing dist/<id>-<version>.brr
   ```

   `mod listing` shows what the list will record of the pack, and names
   anything the list refuses.
3. Tag the commit `v<version>` and publish a GitHub release for the tag
   with the file `<id>-<version>.brr` that `mod pack` wrote. Other files
   in the release are ignored, and a `.zip` in place of the `.brr` is
   refused.

Never change the `.brr` of a release once it is listed. To fix
something, publish a new version.

A version with a pre-release part, like `1.3.0-beta.1`, is a test
version. The list takes test versions only when your entry says
`"prerelease": true`, and Bururu shows them only to players who turn on
"Show test versions".

## 3. The entry

Fork this repo, add `entries/<id>.json`, and open a pull request:

```json
{
  "entry": 1,
  "id": "gravel-rally",
  "repo": "your-login/gravel-rally",
  "official": false,
  "prerelease": false
}
```

| Field | What |
|---|---|
| `entry` | always 1 |
| `id` | your manifest's id; the file is named after it |
| `repo` | `<owner>/<name>` of your repo on github.com |
| `official` | `false`; only the list's maintainers set it |
| `prerelease` | `true` to list test versions too |

The games, authors and permissions come from your manifest, so the entry
does not repeat them.

## 4. What happens next

1. The pull request's check reads your entry, finds your repo and its
   newest release, and posts what it found.
2. A maintainer looks at your repo and merges.
3. Within six hours the poll runs Bururu's own checks on your release
   and opens a pull request with a report: the names, the permissions
   the install dialog will show, the files, and the Lua a reviewer
   should read.
4. A maintainer reads the report against the checklist below and
   merges. The list is published right after, and your mod is in
   Bururu.

## 5. Updates

Publish a new release as in step 2; there is nothing to send. The poll
finds it within six hours and opens a pull request with what changed
against the version before. If the new version reaches more (a new
folder, the screen, the keyboard, a new port, a new required
dependency), it gets the full review again, and Bururu asks the player
before it updates.

The list never changes a listed version. To take one back, publish a
newer one, or ask for a revocation in an issue.

## Rules

- Names: ids are first come, first served. Names and authors must not
  look like another mod's or author's, or like the names in
  `policy/reserved.json`. The check catches look-alikes (0 and o, 1 and
  l, rn and m). A maintainer settles disputes.
- Held: if your repo changes owner, or a download does not match
  GitHub's own digest, or a listed `.brr` changes or goes away, the mod
  is held. Its listed versions stay as they are, and nothing new is taken
  until a maintainer has looked.
- Removed: a mod whose repo is archived, or gone for 30 days, leaves
  Browse. Players who have it keep it, with a note.
- Revoked: a version found to do harm is taken back. Bururu warns the
  players who have it, or switches it off at once for malware.

## The checklist

A maintainer checks these before merging a new mod:

- The pull request's author owns the repo; the repo is public and real.
- Name, id, authors, summary and icon do not pose as Bururu, a game
  studio or another mod; the summary says what the mod does.
- README and CHANGELOG are plain and honest; a licence file is there.

And these for every version, new mods and updates alike:

- Every folder it reads belongs to its game and fits what the mod does;
  the README explains the screen and every UDP port; data leaves the PC
  only when the README names the program it feeds and why.
- The Lua is read in full for a new mod, the changes for an update; no
  hidden or minified code; it does what the README says.
- Dependencies are listed mods, and no new one comes as a surprise.
- The repo and its owner are the same as before.

## Reporting a mod

Use the [report form](https://github.com/getbururu/mod-index/issues/new?template=report-a-mod.yml).
Say which mod and version, what it does wrong, and how to see it.
