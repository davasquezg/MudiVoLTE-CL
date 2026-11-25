[English](THIRD_PARTY.md) | **Español**

> Traducción informativa. En caso de discrepancia prevalece la versión en inglés.

# Componentes de terceros en mudi-imsd

`mudi-imsd` es un único binario enlazado estáticamente (linux/arm64, musl libc).
Todo lo de terceros que está enlazado en él se lista a continuación; los textos de
licencia están en `licenses/` junto a este archivo (última columna).

G.722 (el códec hacia los teléfonos) es una implementación propia del proyecto en Go puro
en `volte/media/g722`, escrita a partir de la Recomendación ITU-T G.722
(11/88); no contiene código de terceros.

La parte en C del binario consiste únicamente en `libopencore-amrnb.a`,
`libopencore-amrwb.a`, `libvo-amrwbenc.a`, `libc.a` de musl y sus archivos de
inicio, diez objetos de `libgcc.a` de GCC y `crtbeginS.o`/`crtendS.o` de GCC
(verificado con un mapa del enlazador antes de agregar `libopencore-amrnb.a`;
queda por verificar con un mapa del enlazador en la próxima compilación
estática si AMR-NB trae más objetos de `libgcc.a`). No se enlaza código de glibc.

| Componente | Versión | Licencia | Fuente | Textos |
|---|---|---|---|---|
| Biblioteca estándar y runtime de Go | go1.26.3 | BSD-3-Clause, más la concesión de patentes de Go (PATENTS) | https://go.googlesource.com/go (tag go1.26.3) | `licenses/go/` |
| github.com/damonto/wwan-go | v0.0.1 | MIT | https://github.com/damonto/wwan-go | `licenses/github.com/damonto/wwan-go/` |
| github.com/emiago/sipgo | v1.7.2 | BSD-2-Clause | https://github.com/emiago/sipgo | `licenses/github.com/emiago/sipgo/` |
| github.com/gobwas/httphead | v0.1.0 | MIT | https://github.com/gobwas/httphead | `licenses/github.com/gobwas/httphead/` |
| github.com/gobwas/pool | v0.2.1 | MIT | https://github.com/gobwas/pool | `licenses/github.com/gobwas/pool/` |
| github.com/gobwas/ws | v1.3.2 | MIT | https://github.com/gobwas/ws | `licenses/github.com/gobwas/ws/` |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | https://github.com/google/uuid | `licenses/github.com/google/uuid/` |
| github.com/icholy/digest | v1.2.0 | MIT | https://github.com/icholy/digest | `licenses/github.com/icholy/digest/` |
| golang.org/x/sync | v0.24.0 | BSD-3-Clause, más PATENTS | https://go.googlesource.com/sync | `licenses/golang.org/x/sync/` |
| golang.org/x/sys | v0.49.0 | BSD-3-Clause, más PATENTS | https://go.googlesource.com/sys | `licenses/golang.org/x/sys/` |
| github.com/vishvananda/netlink | v1.3.1 | Apache-2.0 | https://github.com/vishvananda/netlink | `licenses/github.com/vishvananda/netlink/` |
| github.com/vishvananda/netns | v0.0.5 | Apache-2.0 | https://github.com/vishvananda/netns | `licenses/github.com/vishvananda/netns/` |
| rsc.io/qr | v0.2.0 | BSD-3-Clause (The Go Authors) | https://github.com/rsc/qr (tag v0.2.0) | `licenses/rsc.io/qr/` |
| opencore-amr: libopencore-amrnb (codificador y decodificador AMR-NB), libopencore-amrwb (decodificador AMR-WB) | 0.1.6 (Debian 0.1.6-1) | Apache-2.0 con NOTICE | http://deb.debian.org/debian/pool/main/o/opencore-amr/opencore-amr_0.1.6.orig.tar.gz (proyecto: https://sourceforge.net/projects/opencore-amr/) | `licenses/opencore-amr/` |
| vo-amrwbenc (codificador AMR-WB) | 0.1.3 (Debian 0.1.3-2) | Apache-2.0 con NOTICE (VisualOn, Android Open Source Project; el NOTICE también incluye la licencia Unicode) | http://deb.debian.org/debian/pool/main/v/vo-amrwbenc/vo-amrwbenc_0.1.3.orig.tar.gz (proyecto: https://sourceforge.net/projects/opencore-amr/) | `licenses/vo-amrwbenc/` |
| musl libc | 1.2.5 (Debian 1.2.5-3.1~deb13u1) | MIT (COPYRIGHT lista los autores y los avisos por archivo) | http://deb.debian.org/debian/pool/main/m/musl/musl_1.2.5.orig.tar.gz (proyecto: https://musl.libc.org/) | `licenses/musl/` |
| GCC libgcc.a soft-fp: addtf3, subtf3, multf3, eqtf2, extenddftf2, fixtfsi, fixunstfsi, floatsitf, floatunsitf (long double de 128 bits usado por el printf de musl) | 14.2.0 (Debian 14.2.0-19) | LGPL-2.1-or-later con una excepción especial: los archivos compilados pueden enlazarse en cualquier programa y distribuirse sin las restricciones de la LGPL | http://deb.debian.org/debian/pool/main/g/gcc-14/gcc-14_14.2.0.orig.tar.gz, `libgcc/soft-fp/` | `licenses/gcc/soft-fp.NOTICE` |
| GCC libgcc.a (sfp-exceptions), crtbeginS.o, crtendS.o | 14.2.0 (Debian 14.2.0-19) | GPL-3.0-or-later WITH GCC-exception-3.1 | el mismo tarball, `libgcc/` | `licenses/gcc/COPYING3`, `licenses/gcc/COPYING.RUNTIME` |

Las fuentes de los códecs AMR-WB y AMR-NB incluyen un descargo de responsabilidad sobre patentes ("THIS IS NOT A GRANT OF
PATENT RIGHTS"), incluido como `patent_disclaimer.txt` en ambos directorios
de los códecs.
