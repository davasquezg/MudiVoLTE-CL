**English** | [Español](CONTRIBUTING.es.md)

# Contributing

Thank you for looking into MudiVoLTE. Reports from other operators and other
firmware versions are the most useful thing you can send; patches are welcome
too. Security problems go the private way described in [SECURITY.md](SECURITY.md).

## Reporting a problem

Open an issue with:

- the router model and the GL firmware version, and `mudi-volte version`;
- the country and the operator of the SIM (the operator can be left out);
- what you did, what happened and what you expected;
- `mudi-volte status` and the relevant part of `mudi-volte logs`. The log
  masks phone numbers; the status line and a packet capture do not, so look
  them over before pasting. Never post the IMSI, the IMEI or push tokens.

## Building and testing

The README explains the build (`scripts/build-static.sh`, `package/build.sh`)
and the layout of the repository. Before sending a change, run:

```sh
(cd volte && go vet ./... && go test ./...)
(cd src/imsd && go vet ./... && go test ./...)
python3 src/dsi-helper/tests/helper.test.py   # mudi-dsi-helper (needs Linux and a C compiler)
python3 package/tests/lifecycle.test.py       # mudi-volte setup and restore, the init script
shellcheck -s sh package/install.sh.in package/build.sh package/control/* \
    package/files/usr/sbin/mudi-volte package/files/etc/init.d/mudi-volte scripts/*.sh
sh package/tests/run.sh      # the GL admin panel and LuCI pages (needs node and npm)
luac5.1 -p package/files/usr/lib/oui-httpd/rpc/mudi-volte \
    package/files/usr/share/gl-validator.d/mudi-volte.lua   # the router runs Lua 5.1
python3 package/toolchain/i18n.py extract && python3 package/toolchain/i18n.py check
```

The Go tests run on Linux and macOS; the few that need Linux (the data call
through `mudi-dsi-helper` and its stand-in library, TCP keep-alive) skip
elsewhere. Some tests of `src/imsd` play phones from other addresses on
127/8; macOS answers only on 127.0.0.1 until those are added to `lo0`, and the
tests skip till then. To run them (the addresses last until a restart):

```sh
for a in 2 3 4 9 77 90; do sudo ifconfig lo0 alias 127.0.0.$a up; done
```

The XFRM test of
`volte/ipsec` installs real security associations and runs only on request:
`VOLTE_XFRM_TEST=1 unshare -rn go test ./ipsec/` in `volte/`. A change that
touches the modem, the IMS side or the phones should be tried on a Mudi 7 as
well; say in the pull request what you tried it on.

On Windows (Git Bash) part of this runs too:

- Go: `volte/auth`, `volte/media`, `volte/media/g722` and `volte/srtp` test
  natively. `volte/ipsec` builds on Windows too, but two of its tests
  (`TestRemoveOwnedThroughIP*`) fail there because they run a stub `ip`
  shell script; the rest of `volte` and the daemon build only for Linux, so
  run `go test ./auth/ ./media/... ./srtp/` rather than `go test ./...`.
  `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet ./...` in `volte/` and
  `src/imsd/` type-checks them; their tests run in CI (or on a Linux host).
- `lifecycle.test.py` runs with the Windows Python
  (`python -I package/tests/lifecycle.test.py`) and a `busybox` on `PATH`.
  A `busybox.cmd` that drops its first argument (`ash`) and starts Git's
  `sh.exe` will do:

  ```bat
  @echo off
  shift
  "C:\Program Files\Git\usr\bin\sh.exe" %1 %2 %3 %4 %5 %6 %7 %8 %9
  ```

  The one test that needs POSIX process ids is skipped there.
- `sh package/tests/run.sh` works as is with node and npm on `PATH`.
- `.gitattributes` keeps every text file on LF, also with
  `core.autocrlf=true`. A checkout made before that file came still has CRLF
  files; `git rm --cached -r -q . && git reset -q --hard` (on a clean tree)
  checks them out again with LF. Build packages only from an LF tree: the
  scripts would reach the router with CR otherwise.

The CI (`.github/workflows/ci.yml`) runs all of the above on every push to
`main` and every pull request, and on request for any branch: Actions > CI >
Run workflow, or `gh workflow run ci.yml --ref BRANCH`. On a fork, GitHub
runs no workflows until they are enabled in its Actions tab.

## Releases

`.github/workflows/release.yml` builds and publishes a release: the static
build runs in a Debian 13 (trixie) container on GitHub's arm64 runner, then
`package/build.sh` makes `dist/` with `install.sh` pointing at the release,
and the release gets every file of `dist/` (the `.ipk`, the translation
packages, `install.sh`, `SHA256SUMS`) with the version's section of
`CHANGELOG.md` as its notes. To make one:

1. Set the version in `package/VERSION` (the fork's scheme is in
   [CHANGELOG.md](CHANGELOG.md)) and give its section in `CHANGELOG.md` and
   `CHANGELOG.es.md` the date; commit on `main`.
2. Tag that commit `v` + the version and push the tag:
   `git tag v0.2.0.1 && git push origin v0.2.0.1`. A tag that does not match
   `package/VERSION` stops the workflow.
3. A failed run can be repeated for the same tag: Actions > Release > Run
   workflow; it replaces the files of an existing release.

## Code

- Go is formatted with `gofmt`; shell scripts pass `shellcheck`; comments and
  commit messages are in English. Each commit says what changed and why.
- New third-party code or a new Go module needs a permissive licence (BSD,
  MIT, Apache-2.0 and the like) and an entry in `package/THIRD_PARTY.md` and
  `volte/PROVENANCE.md`. The `volte` library is written from the 3GPP and
  IETF specifications: a change of its behaviour cites the clause it follows.
- Comments cite "§N": sections of the author's engineering log, which is not
  published. New comments should explain the behaviour in place instead;
  `docs/PROTOCOLS.md` is the public account of the live network.
- A change in behaviour comes with a test where the suites allow it
  (`src/imsd/*_test.go` play the phones and the network over 127.0.0.1).

## Translations

The interfaces are written in English and translated through catalogs
(see "Languages" in [package/README.md](package/README.md)). A text the user
sees goes through the marker of its file: `_('...')` in the LuCI page,
`T('...')` (or `NT('...')` in a table, then `T()` where it is shown) in the GL
admin panel page, `T("...", ...)` in the RPC module, `T '...' ARG...` in
`mudi-volte`. The text is one quoted literal, one line, a printf format
(`%s`, `%d`, `%%`) with whole sentences; no newline, tab or backslash. Output
that scripts read stays untranslated.

After changing texts:

```sh
python3 package/toolchain/i18n.py extract   # sources → package/po/templates/mudi-volte.pot
python3 package/toolchain/i18n.py update    # .pot → package/po/<lang>/mudi-volte.po
python3 package/toolchain/i18n.py check     # every text translated, placeholders match
python3 package/tests/i18n.test.py          # the tests of the screen check
```

`check` also compares `package/po/<lang>/mudi-screen-volte.tsv` with the texts
that the screen app (`usr/share/mudi-screen/apps/volte/app.sh`) can print, and
names those without a line in it. It reads `app.sh` by its forms, not as a
shell: the values of `json_add_string` with the keys `title`, `subtitle`,
`text`, `label`, `value`, `confirm` and `confirm_label`, the label and value of
`info` and `section`, what `echo` prints to standard output (`phase_text`,
`ago`, the answer to an action), the values given to the variables these show
and the defaults of `${var:-default}`. Variable parts (`$var`, `$(...)`,
`$((...))`, `{elapsed:...}`) become `{}`; a variable alone, or only names such
as `VoLTE`, is no text. A text printed in another way (`printf`, a
here-document) is not seen: keep `app.sh` to these forms.

A new language: copy the header of `package/po/es/mudi-volte.po` into
`package/po/<lang>/mudi-volte.po`, run `update`, fill in every `msgstr`, and
add `package/po/<lang>/mudi-screen-volte.tsv` for the screen app (MudiScreen's
format: `English<TAB>translation`, `{}` matches any text; the English texts
are the ones `app.sh` prints). `package/build.sh` then makes
`mudi-volte-i18n-<lang>`; give the language its English name in the `case` of
`build.sh`. The documents have Spanish versions next to them (`*.es.md`): a
change to a document should change its translation too, or say in the pull
request that it is still to be done.

## Licence of contributions

MudiVoLTE is published under the PolyForm Noncommercial License 1.0.0 and the
author also licenses it commercially. So that this can go on with outside
contributions, every contribution is made under these terms:

- You license your contribution under the PolyForm Noncommercial License
  1.0.0, like the rest of the project.
- You also grant the project's author, Arthur Valiullin, the right to license
  your contribution, as part of MudiVoLTE, under other terms, including
  commercial ones.
- You certify the [Developer Certificate of Origin 1.1](https://developercertificate.org):
  the contribution is yours to give, or you have the right to give it.

Sign off every commit to record this (`git commit -s` adds the line):

```
Signed-off-by: Your Name <you@example.com>
```

Pull requests without a sign-off cannot be merged.
