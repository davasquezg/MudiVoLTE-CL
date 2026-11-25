[English](CONTRIBUTING.md) | **Español**

# Cómo contribuir

Gracias por interesarte en MudiVoLTE. Los reportes de otros operadores y de otras
versiones de firmware son lo más útil que puedes enviar; los parches también son
bienvenidos. Los problemas de seguridad van por la vía privada descrita en [SECURITY.es.md](SECURITY.es.md).

## Reportar un problema

Abre un issue con:

- el modelo del router y la versión del firmware de GL, y `mudi-volte version`;
- el país y el operador de la SIM (el operador se puede omitir);
- qué hiciste, qué pasó y qué esperabas;
- `mudi-volte status` y la parte relevante de `mudi-volte logs`. El log
  enmascara los números de teléfono; la línea de estado y una captura de paquetes no, así que
  revísalas antes de pegarlas. Nunca publiques el IMSI, el IMEI ni los tokens de push.

## Compilación y pruebas

El README explica la compilación (`scripts/build-static.sh`, `package/build.sh`)
y la estructura del repositorio. Antes de enviar un cambio, ejecuta:

```sh
(cd volte && go vet ./... && go test ./...)
(cd src/imsd && go vet ./... && go test ./...)
python3 src/dsi-helper/tests/helper.test.py   # mudi-dsi-helper (requiere Linux y un compilador de C)
python3 package/tests/lifecycle.test.py       # mudi-volte setup y restore, el script de init
shellcheck -s sh package/install.sh.in package/build.sh package/control/* \
    package/files/usr/sbin/mudi-volte package/files/etc/init.d/mudi-volte scripts/*.sh
sh package/tests/run.sh      # el panel de administración de GL y las páginas de LuCI (requiere node y npm)
luac5.1 -p package/files/usr/lib/oui-httpd/rpc/mudi-volte \
    package/files/usr/share/gl-validator.d/mudi-volte.lua   # el router ejecuta Lua 5.1
python3 package/toolchain/i18n.py extract && python3 package/toolchain/i18n.py check
```

Las pruebas de Go corren en Linux y macOS; las pocas que requieren Linux (la llamada de datos
a través de `mudi-dsi-helper` y su biblioteca sustituta, el keep-alive de TCP) se omiten
en otros sistemas. Algunas pruebas de `src/imsd` simulan teléfonos desde otras direcciones de
127/8; macOS solo responde en 127.0.0.1 hasta que esas direcciones se agregan a `lo0`, y las
pruebas se omiten mientras tanto. Para ejecutarlas (las direcciones duran hasta un reinicio):

```sh
for a in 2 3 4 9 77 90; do sudo ifconfig lo0 alias 127.0.0.$a up; done
```

La prueba de XFRM de
`volte/ipsec` instala asociaciones de seguridad reales y solo se ejecuta si se solicita:
`VOLTE_XFRM_TEST=1 unshare -rn go test ./ipsec/` en `volte/`. Un cambio que
afecte el módem, el lado IMS o los teléfonos también debe probarse en un Mudi 7;
indica en el pull request en qué lo probaste.

En Windows (Git Bash) también corre una parte de esto:

- Go: `volte/auth`, `volte/media`, `volte/media/g722` y `volte/srtp` se
  prueban de forma nativa. `volte/ipsec` también compila en Windows, pero dos
  de sus pruebas (`TestRemoveOwnedThroughIP*`) fallan ahí porque ejecutan un
  script `ip` de reemplazo; el resto de `volte` y el daemon solo compilan para
  Linux, así que corre `go test ./auth/ ./media/... ./srtp/` en vez de
  `go test ./...`. `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go vet ./...` en `volte/` y
  `src/imsd/` revisa sus tipos; sus pruebas corren en el CI (o en un equipo
  con Linux).
- `lifecycle.test.py` corre con el Python de Windows
  (`python -I package/tests/lifecycle.test.py`) y un `busybox` en el `PATH`.
  Sirve un `busybox.cmd` que descarta su primer argumento (`ash`) e inicia el
  `sh.exe` de Git:

  ```bat
  @echo off
  shift
  "C:\Program Files\Git\usr\bin\sh.exe" %1 %2 %3 %4 %5 %6 %7 %8 %9
  ```

  La única prueba que necesita los identificadores de proceso de POSIX se
  omite ahí.
- `sh package/tests/run.sh` funciona tal cual con node y npm en el `PATH`.
- `.gitattributes` mantiene todos los archivos de texto con LF, también con
  `core.autocrlf=true`. Un checkout hecho antes de que existiera ese archivo
  todavía tiene archivos con CRLF;
  `git rm --cached -r -q . && git reset -q --hard` (con el árbol limpio) los
  vuelve a extraer con LF. Construye los
  paquetes solo desde un árbol con LF: si no, los scripts llegarían al router
  con CR.

El CI (`.github/workflows/ci.yml`) ejecuta todo lo anterior en cada push a
`main` y en cada pull request, y a pedido en cualquier rama: Actions > CI >
Run workflow, o `gh workflow run ci.yml --ref RAMA`. En un fork, GitHub no
ejecuta ningún workflow hasta que se habilitan en su pestaña Actions.

## Versiones publicadas

`.github/workflows/release.yml` construye y publica una versión: la
compilación estática corre en un contenedor de Debian 13 (trixie) en el
runner arm64 de GitHub, luego `package/build.sh` genera `dist/` con un
`install.sh` que apunta a esa versión, y la versión publicada recibe todos
los archivos de `dist/` (el `.ipk`, los paquetes de traducción,
`install.sh`, `SHA256SUMS`) con la sección de la versión de `CHANGELOG.md`
como notas. Para publicar una:

1. Fija la versión en `package/VERSION` (el esquema del fork está en
   [CHANGELOG.es.md](CHANGELOG.es.md)) y ponle la fecha a su sección en
   `CHANGELOG.md` y `CHANGELOG.es.md`; haz el commit en `main`.
2. Ponle a ese commit el tag `v` + la versión y sube el tag:
   `git tag v0.2.0.1 && git push origin v0.2.0.1`. Un tag que no coincide con
   `package/VERSION` detiene el workflow.
3. Una ejecución fallida se puede repetir para el mismo tag: Actions >
   Release > Run workflow; reemplaza los archivos de una versión ya
   publicada.

## Código

- El código Go se formatea con `gofmt`; los scripts de shell pasan `shellcheck`; los comentarios y
  los mensajes de commit van en inglés. Cada commit dice qué cambió y por qué.
- El código nuevo de terceros o un módulo Go nuevo requiere una licencia permisiva (BSD,
  MIT, Apache-2.0 y similares) y una entrada en `package/THIRD_PARTY.md` y
  `volte/PROVENANCE.md`. La biblioteca `volte` está escrita a partir de las especificaciones de 3GPP e
  IETF: un cambio en su comportamiento cita la cláusula que sigue.
- Los comentarios citan "§N": secciones del registro de ingeniería del autor, que no está
  publicado. Los comentarios nuevos deben, en cambio, explicar el comportamiento ahí mismo;
  `docs/PROTOCOLS.md` es la descripción pública de la red real.
- Un cambio de comportamiento viene acompañado de una prueba donde las suites lo permitan
  (`src/imsd/*_test.go` simulan los teléfonos y la red sobre 127.0.0.1).

## Traducciones

Las interfaces se escriben en inglés y se traducen mediante catálogos (ver
"Idiomas" en [package/README.es.md](package/README.es.md)). Un texto que ve el
usuario pasa por el marcador de su archivo: `_('...')` en la página de LuCI,
`T('...')` (o `NT('...')` en una tabla, y luego `T()` donde se muestra) en la
página del panel de administración de GL, `T("...", ...)` en el módulo RPC,
`T '...' ARG...` en `mudi-volte`. El texto es un solo literal entre comillas, de
una línea, un formato de printf (`%s`, `%d`, `%%`) con oraciones completas; sin
saltos de línea, tabuladores ni barras invertidas. La salida que leen los
scripts no se traduce.

Después de cambiar textos:

```sh
python3 package/toolchain/i18n.py extract   # código → package/po/templates/mudi-volte.pot
python3 package/toolchain/i18n.py update    # .pot → package/po/<idioma>/mudi-volte.po
python3 package/toolchain/i18n.py check     # todo traducido, marcadores coinciden
python3 package/tests/i18n.test.py          # las pruebas de la revisión de la pantalla
```

`check` además compara `package/po/<idioma>/mudi-screen-volte.tsv` con los
textos que puede imprimir la app de pantalla
(`usr/share/mudi-screen/apps/volte/app.sh`) e indica los que no tienen una línea
en él. Lee `app.sh` por sus formas, no como un shell: los valores de
`json_add_string` con las claves `title`, `subtitle`, `text`, `label`, `value`,
`confirm` y `confirm_label`, la etiqueta y el valor de `info` y `section`, lo
que `echo` imprime en la salida estándar (`phase_text`, `ago`, la respuesta a
una acción), los valores que reciben las variables que estos muestran y los
valores por defecto de `${var:-defecto}`. Las partes variables (`$var`,
`$(...)`, `$((...))`, `{elapsed:...}`) pasan a ser `{}`; una variable sola, o
solo nombres como `VoLTE`, no es un texto. Un texto impreso de otra forma
(`printf`, un here-document) no se detecta: mantén `app.sh` en estas formas.

Un idioma nuevo: copia la cabecera de `package/po/es/mudi-volte.po` en
`package/po/<idioma>/mudi-volte.po`, ejecuta `update`, completa cada `msgstr` y
agrega `package/po/<idioma>/mudi-screen-volte.tsv` para la app de pantalla
(formato de MudiScreen: `inglés<TAB>traducción`, `{}` coincide con cualquier
texto; los textos en inglés son los que imprime `app.sh`). Luego
`package/build.sh` genera `mudi-volte-i18n-<idioma>`; agrega el nombre en inglés
del idioma en el `case` de `build.sh`. Los documentos tienen su versión en
español al lado (`*.es.md`): un cambio en un documento debería cambiar también
su traducción, o indicar en el pull request que queda pendiente.

## Licencia de las contribuciones

MudiVoLTE se publica bajo la PolyForm Noncommercial License 1.0.0 y el
autor también lo licencia comercialmente. Para que esto pueda continuar con contribuciones
externas, toda contribución se realiza bajo estos términos:

- Otorgas una licencia sobre tu contribución bajo la PolyForm Noncommercial License
  1.0.0, como el resto del proyecto.
- También otorgas al autor del proyecto, Arthur Valiullin, el derecho a licenciar
  tu contribución, como parte de MudiVoLTE, bajo otros términos, incluidos
  los comerciales.
- Certificas el [Developer Certificate of Origin 1.1](https://developercertificate.org):
  la contribución es tuya para darla, o tienes el derecho de darla.

Firma cada commit para dejar constancia de esto (`git commit -s` agrega la línea):

```
Signed-off-by: Your Name <you@example.com>
```

Los pull requests sin sign-off no se pueden fusionar.
