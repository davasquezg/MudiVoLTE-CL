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
