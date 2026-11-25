[English](README.md) | **Español**

# Referencia del paquete mudi-volte

Este directorio construye el paquete OpenWrt `mudi-volte`. La instalación y el
uso cotidiano se describen en el [README](../README.es.md) en la raíz del
repositorio; esta página es la referencia completa.

## Todos los comandos

```
mudi-volte setup [-y] [--lte-only|--keep-3g] [--phone NAME] [--detach]
mudi-volte status
mudi-volte missed [clear]
mudi-volte sms [--json]
mudi-volte phone add NAME | del NAME | show NAME | list | url NAME | ext NAME NUMBER | screen NAME [SECONDS]
mudi-volte push [login NAME | off]
mudi-volte logs [-f | -a]
mudi-volte start | stop | restart
mudi-volte modem [status | builtin [-y] [--detach]]
mudi-volte uninstall [-y] [--keep-config]
mudi-volte version
```

| Comando | Qué hace |
|---|---|
| `setup` | Revisa el router, el módem y la SIM, crea una cuenta de teléfono, cambia el módem al modo External VoLTE con voz solo sobre IMS, desactiva el modo de suspensión del router y reinicia el router una vez. Cuando el módem rechaza un valor, setup restablece lo que escribió y falla: sin reinicio, y el servicio y la configuración de suspensión quedan como estaban. `-y` responde la pregunta por sí mismo |
| `setup --lte-only` | Además mantiene el módem en 4G/5G. Rara vez es necesario: cuesta el internet donde solo llega 3G. `--keep-3g` es el valor por defecto |
| `status` | Si la SIM está registrada, qué teléfonos están conectados, cuántas llamadas se perdieron |
| `missed` | Llamadas entrantes que ningún teléfono contestó, de la más reciente a la más antigua: hora, número, motivo |
| `missed clear` | Vacía la lista de llamadas perdidas |
| `sms` | SMS a la SIM, del más reciente al más antiguo: los que recibió el módem (el spool de smsd de GL, `/etc/spool/sms/incoming`, que muestra la página SMS del panel de administración de GL) y los que llegaron por IMS. `--json` es para la página web |
| `phone add NAME` / `del NAME` | Agrega o elimina una cuenta de teléfono |
| `phone list` | Cuentas de teléfono con sus números cortos, en línea o no |
| `phone show NAME` | El código QR del teléfono y su configuración |
| `phone url NAME` | Solo el enlace de configuración del teléfono |
| `phone ext NAME NUMBER` | Asigna otro número corto a un teléfono (`-` lo elimina) |
| `phone screen NAME [SECONDS]` | El código QR en la pantalla del router (90 s por defecto; luego se cierra solo) |
| `push` | Push a teléfonos dormidos: la cuenta de linphone.org y los teléfonos que se pueden despertar |
| `push login NAME` | Configura la cuenta de linphone.org que firma los push; pide su contraseña en la terminal (por SSH: `ssh -t`), la guarda mediante `uci batch` desde stdin y reinicia el servicio |
| `push off` | Elimina la cuenta: los iPhone reciben llamadas solo mientras Linphone está abierto, los teléfonos Android mientras su servicio está corriendo |
| `logs` / `logs -f` / `logs -a` | Log del servicio: últimas líneas / seguir / todo |
| `start` / `stop` / `restart` | Controlan el servicio |
| `modem status` | Configuración del módem, incluida la de voz que cambió setup |
| `modem builtin` | Devuelve VoLTE al cliente propio del módem, restablece la configuración de suspensión, apaga el servicio y reinicia el router; el paquete se queda |
| `uninstall` | Lo que hace `modem builtin` y, además, elimina el paquete con su configuración y sus teléfonos. `--keep-config` conserva `/etc/config/mudi-volte`. Eliminar el paquete con `opkg remove` o en LuCI > System > Software hace lo mismo, salvo el reinicio |
| `version` | Versión del paquete |

## Configuración

`/etc/config/mudi-volte` (UCI) explica cada opción en sus comentarios: quién puede
llamar (`incoming`, `allow`), a dónde pueden llamar y enviar SMS los teléfonos (`dial`),
códecs, tiempo de timbre, el número de prueba de eco, SMS en los teléfonos (`sms`), el puerto
SIP, el puerto de configuración por QR, el cifrado (`encryption`, `tls_port` y `plain`:
si UDP y TCP quedan abiertos junto a TLS), la cuenta para
push a teléfonos dormidos (`push_login`, `push_password`; `push_url` y `push_domain`
solo para un servidor FlexiAPI distinto de subscribe.linphone.org), el nivel de log
y la SIM de la línea (`sim_slot`: 1, o 2 para la ranura 2 y la eSIM integrada; la
configuración elige la primera ranura con una SIM lista y la escribe; todos los
comandos AT, la llamada de datos IMS y la cola de SMS usan su suscripción).
Las páginas web editan el mismo archivo. Después de `uci commit mudi-volte`, ejecuta
`mudi-volte restart`.

Para una red que los necesite, el archivo también trae ajustes del operador, comentados:
sin definir o vacío significa automático, desde la red, el módem y la SIM.

| Opción UCI | Campo del daemon | Qué ajusta | Sin definir |
|---|---|---|---|
| `list pcscf` | `pcscf` | direcciones del P-CSCF (IPv4) en el orden en que se prueban | las que anuncia la red (CGCONTRDP) |
| `pcscf_port` | `pcscf_port` | puerto del primer REGISTER, sin protección | 5060 |
| `domain` | `domain` | dominio IMS de origen | desde la ISIM, o si no armado a partir del IMSI |
| `imei` | `imei` | IMEI que se entrega a la red (15 dígitos) | el del módem (CGSN) |
| `smsc` | `smsc` | centro de servicio de SMS (+ y hasta 15 dígitos) | desde la SIM, o si no desde el primer SMS entrante |
| `network_codecs` | `codecs` | códecs que se ofrecen a la red, el mejor primero: `AMR-WB`, `PCMA`, `PCMU` | todos (`codecs` es la lista de los teléfonos) |
| `ims_apn` | `ims_apn` | APN del perfil del módem que se usa para IMS | `ims` |
| `ims_profile` | `ims_profile` | el número (cid) de ese perfil, sin buscar el APN | el perfil con `ims_apn`, o si no el 2 |
| `list ipsec_offer` | `ipsec_offers` | pares de algoritmos IPsec que se ofrecen al P-CSCF, el mejor primero, como `integridad/cifrado` | los seis, ver más abajo |
| `loose_inbound_src_port` | `loose_inbound_src_port` | 0 fija el puerto de origen del P-CSCF en las políticas IPsec de entrada | sin fijar |

Los pares IPsec son `hmac-md5-96` o `hmac-sha-1-96` con `null`, `aes-cbc` o
`des-ede3-cbc`. Sin definir, el daemon ofrece los seis, primero `hmac-md5-96/null` (el
único que se ofrecía antes y el que eligió la red probada), luego
`hmac-sha-1-96/null` y después los pares con cifrado. Estos no se han probado en
una red real, y puede que al kernel del router le falten sus cifradores (por
verificar en el router): si falla la instalación de las SA IPsec después de que la
red eligió un cifrado, el daemon deja una advertencia en el log y se registra de
nuevo ofreciendo solo los pares sin cifrado. Para no ofrecer cifrado nunca, deja en
la lista solo esos dos pares.
El daemon revisa estos valores al iniciar: con uno que no puede usar no inicia, e
indica la opción en el log y en `mudi-volte status`, con su campo del daemon
seguido de la opción UCI cuando no coinciden, por ejemplo
`codecs (UCI network_codecs)` (un `pcscf_port` o un `ims_profile` que no es un
número, solo en el log). El log también indica qué perfil del
módem tomó para IMS y por qué.

## Idiomas

Los textos están en inglés. Un paquete de traducción, `mudi-volte-i18n-<idioma>`
(por ahora `mudi-volte-i18n-es`, español; arquitectura `all`, también provee
`luci-i18n-mudi-volte-<idioma>`), traduce:

| Dónde | Idioma que sigue |
|---|---|
| Línea de comandos y configuración | `mudi-volte.main.lang` |
| Página en el panel de administración de GL y sus mensajes de error | `mudi-volte.main.lang`; si no está definido, el idioma del panel y, si no, el del navegador |
| Página en LuCI | El idioma de LuCI (System > System > Language) |
| App en la pantalla del router (MudiScreen) | El idioma de la pantalla (Settings > Language) |

La primera instalación de un paquete de traducción define `mudi-volte.main.lang`
con su idioma, salvo que ya haya uno; al desinstalarlo, se borra. Para volver al
inglés: `uci set mudi-volte.main.lang=en; uci commit mudi-volte`. El instalador
instala uno junto con la configuración:
`wget -qO- .../install.sh | MUDI_VOLTE_LANG=es sh`; con los paquetes copiados al
router, `opkg install mudi-volte-i18n-es_*_all.ipk` después del principal. La
salida que leen los scripts (`version`, `--json`, `phone url`, `phone list`) y el
log del servicio quedan en inglés. Para agregar un idioma, ver
[CONTRIBUTING.es.md](../CONTRIBUTING.es.md#traducciones).

## SMS en los teléfonos

Con `option sms '1'` (el valor por defecto), el servicio lee el spool de smsd de GL (los
directorios en `/tmp/smstools.cfg`) cada 5 segundos y los SMS que llegaron por
IMS, y envía cada SMS nuevo a cada cuenta de teléfono como un SIP MESSAGE desde el
número del remitente. `/etc/mudi-volte/sms-phones.json` registra qué teléfonos todavía
esperan qué SMS, de modo que un reinicio no los pierde ni los repite; un teléfono que
está fuera los recibe cuando se vuelve a registrar, durante hasta 7 días. En el primer inicio
(sin ese archivo), los SMS que ya están en el router se omiten. Desactivar la opción
borra el archivo.

Un MESSAGE desde un teléfono va al número de eco (de vuelta al remitente), a
otro teléfono (por nombre o número corto), o sale como SMS: el número debe pasar
`dial`, y el texto puede ocupar hasta 10 SMS. El SMS se convierte en un archivo en el directorio
`outgoing` de smsd con los encabezados que escribe `sendsms` de GL (`To`, `Alphabet`
UTF-8 o UCS, `Bus`, `Slot`, `To_TOA`); el teléfono recibe `200` una vez que smsd lo mueve
a `sent`, `500` cuando termina en `failed`, y `202` cuando smsd no ha
decidido en 20 segundos (en ese caso, una falla posterior le llega al teléfono como
mensaje). Las notificaciones de escritura y los acuses de entrega se aceptan y se descartan;
los archivos reciben `415`.

## Códigos QR y enlaces de configuración

El código QR de un teléfono enlaza a `http://<router>:8060/p/<token>`, donde el router
le entrega al teléfono su configuración de Linphone. Cada código mostrado (`mudi-volte
phone show`, `phone url`, `phone screen`, las páginas web) es un enlace nuevo con 96
bits aleatorios. Funciona una sola vez, dentro de 15 minutos (en la pantalla del router, mientras
se muestre el código), y solo en la red propia del router. La primera
descarga lo consume junto con los demás enlaces del teléfono; eliminar el teléfono o
asignarle una contraseña nueva también los termina. Los enlaces son archivos en
`/var/run/mudi-volte/links`, así que un reinicio también los termina. La configuración
detrás de un enlace contiene un hash de la contraseña del teléfono que funciona como la
contraseña misma, así que no compartas un enlace vigente. Diez enlaces rechazados desde una
dirección bloquean esa dirección durante diez minutos. `option provision_port '0'` desactiva
los enlaces.

Con `option encryption '1'` (el valor por defecto), el enlace le da a Linphone la cuenta
sobre TLS en `tls_port` (5061) y configura en la sección `[sip]` de Linphone:
`media_encryption=srtp`, `media_encryption_mandatory=1`,
`verify_server_certs=0` y `verify_server_cn=0`. Estas entradas se aplican a todas las
cuentas de ese Linphone; liblinphone no tiene una configuración por cuenta para ellas ni
una forma de confiar en un certificado desde su configuración. Con
`option encryption '0'`, el enlace entrega UDP y restablece las cuatro entradas a los
valores por defecto de Linphone (`none`, `0`, `1`, `1`), de modo que un teléfono que se saca de TLS no
rechace todas las llamadas.

## Cifrado hacia los teléfonos

El daemon escucha SIP sobre TLS (1.2 y 1.3) en `tls_port`, junto a UDP
y TCP en `port`. Con `option plain '0'`, los puertos UDP y TCP permanecen cerrados
y los teléfonos llegan solo por TLS: entonces nada de lo que un teléfono firma se puede leer desde
el Wi-Fi, pero cada teléfono debe configurarse por código QR. El certificado es autofirmado, ECDSA P-256, para la dirección
LAN, válido por diez años: el daemon lo crea al iniciar en
`/etc/mudi-volte/tls.crt` y `tls.key` cuando faltan, están dañados, son para
otra dirección o están a 30 días o menos de su vencimiento. La línea de log
`SIP: TLS for the phones` muestra su huella SHA-256.

Las claves SRTP viajan en el SDP (SDES, RFC 4568), por lo que solo dentro de TLS:

| La oferta del teléfono | Sobre TLS | Sobre UDP o TCP |
|---|---|---|
| `RTP/SAVP` con `a=crypto` | SRTP, con una clave propia para la suite y el tag de la primera línea que soportamos | `488` |
| `RTP/AVP` con `a=crypto` | SRTP en `RTP/AVP` | RTP plano |
| `RTP/AVP` | RTP plano | RTP plano |
| DTLS-SRTP (`UDP/TLS/RTP/SAVP`) | `488` | `488` |

Suites: `AES_CM_128_HMAC_SHA1_80`, `AES_CM_128_HMAC_SHA1_32`,
`AES_256_CM_HMAC_SHA1_80`, `AES_256_CM_HMAC_SHA1_32`; sin MKI, tasa de derivación
de claves 0. Las llamadas entrantes le ofrecen a un teléfono en TLS `RTP/SAVP` con
`AES_CM_128_HMAC_SHA1_80` y `AES_256_CM_HMAC_SHA1_80`, y a los teléfonos en UDP, RTP
plano; un teléfono en TLS que responde sin una clave nuestra recibe un BYE. Un re-INVITE
conserva las claves (una clave nueva del teléfono reemplaza la antigua); uno que las
elimina recibe `488`. RTCP no se envía ni se lee, por lo que no se usa SRTCP.
Las solicitudes a un teléfono en TLS o TCP (INVITE, MESSAGE, BYE) van por la
conexión que este abrió; cuando esa conexión ya no existe, el teléfono cuenta como
desconectado en lugar de ser llamado. `mudi-volte status` y `phone list` marcan
los teléfonos en TLS como cifrados; `state.json` los lista en `phones_tls`.

## Push a teléfonos dormidos

Linphone en iOS se detiene en segundo plano y cierra su conexión; su
registro se mantiene hasta que expira. Linphone en Android mantiene un servicio en segundo plano
y su conexión (el código QR activa el servicio), hasta que el
sistema o un ahorrador de batería lo detiene. Cada REGISTER lleva los parámetros de push
del dispositivo en el Contact (RFC 8599: `pn-provider`, `pn-param`, `pn-prid`); el
daemon los guarda por dispositivo (`+sip.instance`) en `/etc/mudi-volte/push.json`,
escribiendo el archivo solo cuando cambian, y al iniciar descarta los dispositivos de los teléfonos
eliminados.

Con `push_login` configurado, una llamada (desde la red, o desde otro teléfono a este)
va de inmediato a los dispositivos con una conexión activa y, por cada dispositivo
sin ella, a la FlexiAPI de Belledonne: `POST https://subscribe.linphone.org/api/push_notification`
con `type=call`, un Call-ID propio del daemon (`mv-` y 24 dígitos hexadecimales) y
los parámetros de push: para un iPhone (`pn-provider=apns`), el token VoIP y el
topic `.voip` (PushKit); para Android (`pn-provider=fcm`), el número del proyecto de Firebase
y el token FCM tal como vienen (un mensaje FCM de alta prioridad). FlexiAPI responde `401`
con desafíos Digest (RFC 7616); el daemon firma la solicitud con la
cuenta (SHA-256, o si no MD5) y el encabezado `From: sip:<login>@sip.linphone.org`.
El Linphone despertado se vuelve a registrar sobre TLS y, justo después del `200 OK`, el
daemon le envía el INVITE con ese Call-ID. A un dispositivo que no ha vuelto
después de 3 s se le envía un push una vez más (iOS puede haber descargado Linphone, y el primer
push solo lo inicia). Un dispositivo que recibió push y vuelve después de que terminó el timbre
recibe el INVITE y de inmediato un CANCEL, dentro de 25 s, para que Linphone cierre
la llamada como perdida. Cualquier teléfono que se registre mientras una llamada está sonando también recibe esa llamada.
El log muestra `push sent to the phone` con el tiempo que tardó FlexiAPI y
`the phone came back, ringing it`; los tokens nunca se registran en el log.

`phone screen` detiene la app de pantalla de fábrica mientras dibuja el código y la vuelve a iniciar
después. Un toque cierra el código antes de tiempo solo si la pantalla ya estaba
encendida: el panel táctil informa solo a la app de fábrica.

## La configuración propia del daemon

El script de init convierte la configuración UCI en `/var/run/mudi-volte/imsd.json`
e inicia `mudi-imsd -mode service -config` con él (`/etc/init.d/mudi-volte
json` imprime lo mismo; contiene las contraseñas de los teléfonos). El daemon lee unos
pocos campos que el paquete nunca escribe (los ajustes del operador de más arriba
los escribe solo cuando están definidos). Sirven para el desarrollo en un router
sin teléfonos, con una configuración propia y `-mode daemon` o `-mode
register-once` (el encabezado de `src/imsd/main.go` lista los modos):

| Campo | Qué hace |
|---|---|
| `record_dir` | Un directorio: una llamada entrante que el daemon contesta por sí mismo, sin el puente SIP, se graba ahí como archivo WAV (16 kHz, mono). Sin efecto mientras esté presente el bloque `sip`, como en el servicio que ejecuta el paquete |
| `play_file` | Un archivo WAV (8 o 16 kHz, mono) que se reproduce en esa llamada en lugar de silencio; con los mismos límites |
| `max_call_sec` | Corta cada llamada después de esta cantidad de segundos, también las puenteadas; 0 = nunca |

Nada en el paquete graba llamadas: el servicio hace de puente para el audio entre
la red y los teléfonos y no guarda nada de él.

## Archivos en el router

| Ruta | Qué es |
|---|---|
| `/usr/sbin/mudi-imsd` | El cliente VoLTE y el servidor SIP para los teléfonos, un binario estático, ejecutado como el servicio procd `mudi-volte` |
| `/usr/sbin/mudi-volte` | Configuración y gestión |
| `/etc/config/mudi-volte` | Configuración y cuentas de teléfono |
| `/etc/mudi-volte/sms-inbox.jsonl` | SMS que llegaron por IMS, un objeto JSON por línea (el operador puede entregar SMS de esta forma en lugar de al módem) |
| `/etc/mudi-volte/missed.jsonl` | Llamadas perdidas, un objeto JSON por línea, con los números completos; al superar 64 KB se recorta a las 200 más recientes |
| `/etc/mudi-volte/sms-phones.json` | Qué teléfonos todavía esperan qué SMS (solo hashes, horas y nombres de teléfonos) |
| `/etc/mudi-volte/push.json` | Parámetros de push de los teléfonos (proveedor, app, token), por dispositivo; modo 0600 |
| `/etc/mudi-volte/tls.crt`, `tls.key` | El certificado del router para SIP sobre TLS |
| `/var/run/mudi-volte/links/` | Los enlaces de un solo uso detrás de los códigos QR, un archivo cada uno (nombre del teléfono, verificación de contraseña, vencimiento), que desaparecen tras un reinicio o una actualización de firmware; modo 0700 |
| `/etc/mudi-volte/modem.orig`, `lpm.orig` | Configuración del módem y de suspensión de antes de setup; uninstall la restablece |
| `/lib/upgrade/keep.d/mudi-volte` | Pide a una actualización del firmware de GL con Keep Settings, y a `sysupgrade -b`, que conserven `/etc/config/mudi-volte` y `/etc/mudi-volte` |
| `/var/log/mudi-volte.log` | Log del servicio (aproximadamente el último día; el log del sistema guarda solo minutos) |
| `/www/views/gl-sdk4-ui-mudi-volte.common.js.gz`, `/usr/share/oui/menu.d/mudi-volte.json`, `/usr/lib/oui-httpd/rpc/mudi-volte` | La página en el panel de administración de GL (Applications > Calls & SMS): un componente Vue 2 que el panel carga por nombre, su entrada de menú y su módulo RPC (Lua). Solo el administrador del panel puede invocarlo |
| `/www/luci-static/resources/view/mudi-volte/`, `/usr/share/luci/menu.d/`, `/usr/share/rpcd/acl.d/luci-app-mudi-volte.json` | La página en LuCI (Services > Calls & SMS) |
| `/usr/share/nftables.d/chain-pre/input/20-mudi-volte.nft` | Dos reglas de firewall que dejan entrar el tráfico IPsec del operador en la conexión IMS |
| `/usr/share/doc/mudi-volte/` | `LICENSE`, `NOTICE`, `THIRD_PARTY.md` y los textos de licencia de terceros |

Las horas de las llamadas perdidas y de las líneas de log son las del reloj del router en ese
momento. Tras un arranque, el router inicia su reloj a partir del archivo más reciente bajo
`/etc`, atrasado al menos tanto tiempo como el router estuvo apagado, y lo corrige una vez que
alcanza un servidor NTP; una llamada perdida antes de eso conserva la hora que mostraba
el reloj. Cuánto tiempo lleva el servicio en su estado (`status`, las páginas web)
y los 7 días que un SMS espera a un teléfono se cuentan con un reloj que esta
corrección no mueve.

## Este directorio

| Ruta | Qué es |
|---|---|
| `VERSION` | Versión del paquete |
| `build.sh` | Construye `dist/`: el `.ipk`, un `.ipk` de traducción por cada catálogo en `po/`, `install.sh` y `SHA256SUMS`, a partir de `build/static/mudi-imsd` (generado por `scripts/build-static.sh`). `MUDI_VOLTE_BASE_URL` define desde dónde descarga los paquetes `install.sh` |
| `install.sh.in` | El instalador; `build.sh` completa la versión, la dirección de descarga y los checksums |
| `control/` | Archivos de control de opkg: metadatos del paquete y scripts del mantenedor |
| `control-i18n/` | Lo mismo para los paquetes de traducción (`@LANG@` se completa por idioma) |
| `po/` | Traducciones: `templates/mudi-volte.pot` (generado desde el código), `<idioma>/mudi-volte.po` y `<idioma>/mudi-screen-volte.tsv` para la app de pantalla (formato de catálogo de MudiScreen) |
| `files/` | Todo lo que se instala en el router excepto el binario y los archivos de licencia; `build.sh` comprime con gzip `www/views/` como lo espera el panel de administración de GL |
| `tests/` | `run.sh`: la página del panel de administración de GL con Vue 2 en node, y la página de LuCI con los módulos de LuCI como stubs (requiere node y npm) |
| `toolchain/` | Toolchain de musl y bibliotecas AMR-WB para la compilación estática, el recolector de licencias e `i18n.py` (extrae, actualiza, revisa y construye las traducciones) |
| `THIRD_PARTY.md`, `licenses/` | Componentes de terceros dentro de `mudi-imsd` y sus textos de licencia |
