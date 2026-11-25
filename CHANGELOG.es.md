[English](CHANGELOG.md) | **Español**

# Registro de cambios

Cambios notables de MudiVoLTE. La versión está en `package/VERSION`; los
paquetes y sus checksums están en la
[página de versiones](https://github.com/davasquezg/MudiVoLTE-CL/releases) de este fork. Las
versiones hasta la 0.2.0 son las del proyecto original,
[xarvel/MudiVoLTE](https://github.com/xarvel/MudiVoLTE/releases).

El fork numera sus versiones como la versión del original sobre la que se
construyen más un cuarto número: 0.2.0.1 es la primera versión del fork
sobre la 0.2.0 del original. Solo dígitos y puntos, así que la misma cadena
sirve como versión de opkg (`0.2.0.1-1`), en los nombres de archivo, en el
tag de git (`v0.2.0.1`; git no acepta `~`) y en los nombres de los archivos
de la versión publicada (GitHub renombra los que tienen caracteres
especiales). opkg compara las versiones como Debian: 0.2.0.1 es más nueva
que la 0.2.0 del original y más antigua que una 0.2.1 del original, así que
una versión del original publicada después es una actualización; la
versión del fork sobre ella es la 0.2.1.1, de nuevo más nueva.

## 0.2.0.1 (2026-11-25, versión de prueba: por probar en el router)

Primera versión del fork, sobre la 0.2.0 del original. Lo que cambia para
Chile, y lo que sigue pendiente, está en los
[issues del fork](https://github.com/davasquezg/MudiVoLTE-CL/issues).

- Español: la documentación, y textos traducibles en la herramienta de línea
  de comandos y su configuración, la página del panel de administración de
  GL, la página de LuCI y la app de la pantalla del router. Los textos de un
  idioma vienen en su propio paquete, `mudi-volte-i18n-<idioma>`
  (`MUDI_VOLTE_LANG=es` en el instalador); el primero es el español.
- La línea puede ir en la SIM 2 o en la eSIM integrada, no solo en la SIM 1:
  la configuración toma la primera ranura con una SIM lista,
  `mudi-volte.main.sim_slot` elige otra. Aún no se ha probado en un router.
- El instalador, las versiones publicadas y los reportes de seguridad son
  los del fork (`davasquezg/MudiVoLTE-CL`); un workflow de publicación
  construye y publica los paquetes a partir de un tag `v*`.

## 0.2.0 (2026-11-23)

- La línea telefónica en la pantalla táctil del router: el paquete trae una app para
  MudiScreen, una interfaz de usuario abierta para la pantalla del Mudi 7
  (`/usr/share/mudi-screen/apps/volte`). Muestra la línea, los teléfonos y
  las llamadas perdidas, y una llamada perdida enciende la pantalla con un aviso que
  no lleva ningún número. Sin MudiScreen nada cambia.
- Una llamada en la línea se muestra mientras está en curso: `mudi-volte status` tiene una línea para
  ella (con el número enmascarado), y en MudiScreen una llamada entrante enciende la
  pantalla con un aviso hasta que se contesta, y la tarjeta, la pantalla de bloqueo y
  la barra de estado muestran la llamada con su tiempo transcurrido. La pantalla de bloqueo y
  los avisos no muestran ningún número.
- `mudi-volte phone screen` devuelve la pantalla al programa que la tenía,
  MudiScreen o la pantalla de GL.iNet.
- MudiScreen es opcional: sin él, o con la pantalla propia de GL.iNet en uso,
  la línea funciona como antes.
- El modo avión se respeta. Con la radio apagada en el router (el
  modo avión de GL, desde el panel de administración o la pantalla táctil), el servicio espera
  hasta que vuelva a encenderse, como "the radio is off (airplane mode)". Antes,
  tomaba la falta de conexión como una falla y, tras dos de ellas, volvía a encender la
  radio (AT+CFUN=4, luego 1), terminando el modo avión en cuestión de minutos.

## 0.1.0 (2026-11-14)

Primera versión pública: llamadas y SMS en el GL.iNet Mudi 7 a través de Linphone
en el Wi-Fi del router, configuración de teléfonos mediante códigos QR de un solo uso, llamadas cifradas
(SIP sobre TLS, SRTP) con una opción para cerrar los puertos SIP sin cifrar,
push para teléfonos en segundo plano a través de subscribe.linphone.org, páginas en
el panel de administración de GL y en LuCI, llamadas fuera de casa sobre Tailscale. Una actualización
del firmware de GL con Keep Settings conserva los teléfonos y los datos del
paquete, de modo que al instalar el paquete de nuevo la línea vuelve.
