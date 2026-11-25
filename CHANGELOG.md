**English** | [Español](CHANGELOG.es.md)

# Changelog

Notable changes to MudiVoLTE. The version is in `package/VERSION`; the
packages and their checksums are on the
[releases page](https://github.com/davasquezg/MudiVoLTE-CL/releases) of this fork. Versions up to
0.2.0 are those of the upstream project,
[xarvel/MudiVoLTE](https://github.com/xarvel/MudiVoLTE/releases).

The fork numbers its releases as the upstream version they build on plus a
fourth number: 0.2.0.1 is the fork's first release on top of upstream 0.2.0.
Only digits and dots, so the same string works as the opkg version
(`0.2.0.1-1`), in the file names, in the git tag (`v0.2.0.1`; git refuses
`~`) and in the names of the release assets (GitHub renames assets with
special characters). opkg compares versions the Debian way: 0.2.0.1 is
newer than upstream 0.2.0 and older than an upstream 0.2.1, so an upstream
release that came later is an upgrade; the fork's release on top of it is
0.2.1.1, newer again.

## 0.2.0.1 (2026-11-25, pre-release: to test on the router)

First release of the fork, on top of upstream 0.2.0. What changes for
Chile, and what is still open, is in the
[fork's issues](https://github.com/davasquezg/MudiVoLTE-CL/issues).

- Spanish: the documentation, and translatable texts in the command line
  tool and its setup, the GL admin panel page, the LuCI page and the app on
  the router's screen. The texts of a language come in their own package,
  `mudi-volte-i18n-<lang>` (`MUDI_VOLTE_LANG=es` in the installer); the
  first one is Spanish.
- The line can be on SIM 2 or the built-in eSIM, not only on SIM 1: setup
  takes the first slot with a ready SIM, `mudi-volte.main.sim_slot`
  chooses another. Not tested on a router yet.
- The installer, the releases and security reports are the fork's
  (`davasquezg/MudiVoLTE-CL`); a release workflow builds and publishes the
  packages from a `v*` tag.

## 0.2.0 (2026-11-23)

- The phone line on the router's touchscreen: the package brings an app for
  MudiScreen, an open user interface for the Mudi 7 display
  (`/usr/share/mudi-screen/apps/volte`). It shows the line, the phones and
  the missed calls, and a missed call lights the display with a notice that
  carries no number. Without MudiScreen nothing changes.
- A call on the line shows while it is on: `mudi-volte status` has a line for
  it (with the number masked), and on MudiScreen an incoming call lights the
  display with a notice until it is answered, and the card, the lock screen
  and the status bar show the call with its running time. The lock screen and
  the notices show no number.
- `mudi-volte phone screen` gives the display back to the program that had
  it, MudiScreen or GL.iNet's screen.
- MudiScreen is optional: without it, or with GL.iNet's own screen in use,
  the line works as before.
- Airplane mode is left alone. With the radio off on the router (GL's
  airplane mode, from the admin panel or the touchscreen), the service waits
  until it is on again, as "the radio is off (airplane mode)". Before, it
  took the missing connection for a failure, and after two of them turned the
  radio back on (AT+CFUN=4, then 1), ending the airplane mode within minutes.

## 0.1.0 (2026-11-14)

First public release: calls and SMS on the GL.iNet Mudi 7 through Linphone
on the router's Wi-Fi, phone setup by one-time QR codes, encrypted calls
(SIP over TLS, SRTP) with an option to close the unencrypted SIP ports,
pushes for phones in the background through subscribe.linphone.org, pages in
the GL admin panel and LuCI, calls away from home over Tailscale. A GL
firmware update with Keep Settings keeps the phones and the data of the
package, so installing the package again brings the line back.
