[English](PROTOCOLS.md) | **Español**

# PROTOCOLS.md — Referencia de protocolos de MudiVoLTE

Referencia para desarrolladores que reproducen o mantienen el cliente VoLTE del lado del host
en el router GL.iNet Mudi 7 (GL-E5800, módem Quectel RG650V, OpenWrt).
Solo hechos confirmados por el registro de ingeniería del proyecto y por el código.
§N cita una sección del registro de ingeniería interno del proyecto, que no está publicado:
indica la fuente de un hecho, no un documento que se pueda leer.
Los datos personales (números de teléfono, IMSI, IMEI, direcciones, tokens) se reemplazan por `<...>`.
Las direcciones de la red del operador (P-CSCF, MSC, gateway de medios, nuestra propia dirección en
la conexión IMS) se reemplazan por direcciones de documentación (RFC 5737): `192.0.2.x` para
el lado del operador, `198.51.100.x` para el nuestro.

---

## Contenido

1. [Visión general de la arquitectura](#1-visión-general-de-la-arquitectura)
2. [Propietario de Quectel — External VoLTE Stack](#2-propietario-de-quectel--external-volte-stack)
3. [Propietario de GL.iNet — firmware del Mudi](#3-propietario-de-glinet--firmware-del-mudi)
4. [Protocolos estándar — implementación](#4-protocolos-estándar--implementación)
   - 4.1 [Registro IMS (sec-agree, IPsec, AKA)](#41-registro-ims-sec-agree-ipsec-aka)
   - 4.2 [Llamadas entrantes y salientes](#42-llamadas-entrantes-y-salientes)
   - 4.3 [Medios (RTP, AMR-WB, AMR, G.722, G.711, DTMF)](#43-medios-rtp-amr-wb-amr-g722-g711-dtmf)
   - 4.4 [SMS sobre IP](#44-sms-sobre-ip)
   - 4.5 [Puente SIP para teléfonos de la LAN (Wi-Fi del Mudi)](#45-puente-sip-para-teléfonos-de-la-lan-wi-fi-del-mudi)
5. [Problemas conocidos](#5-problemas-conocidos)
6. [Referencias](#6-referencias)

---

## 1. Visión general de la arquitectura

### Componentes

| Componente | Función |
|-----------|------|
| **Quectel RG650V** | El módulo 5G del GL-E5800. El Linux del router corre en el propio procesador de aplicaciones del módulo (puerto AT `/dev/smd11`; `AT+CFUN=1,1` reinicia el router completo). En modo "External VoLTE Stack" el módulo entrega el PDN IMS al host y ejecuta AKA en la SIM mediante `AT+CSIM`. Su propio cliente IMS está apagado. |
| **OpenWrt (GL-E5800)** | Host del daemon `mudi-imsd`. Aquí residen `rmnet_dataN` con la dirección IMS, XFRM/IPsec del kernel y nftables. También es el punto de acceso Wi-Fi para los teléfonos. |
| **mudi-imsd** | Daemon en Go que implementa un UE IMS del lado del host: registro, llamadas entrantes/salientes, SMS, puente SIP para teléfonos de la LAN. Binario arm64 estático (~8 MB), sin dependencias externas (musl). |
| **P-CSCF** | El servidor proxy del operador (dos direcciones, obtenidas mediante `AT+CGCONTRDP`). SIP/TCP sobre el PDN IMS. El primer P-CSCF puede conectarse desde un puerto de origen no estándar (§82, §81). |
| **S-CSCF / I-CSCF / núcleo IMS** | La infraestructura IMS estándar del operador. |
| **Teléfonos en Wi-Fi** | Linphone (Android / iOS) o cualquier cliente SIP. Se conectan al registrar LAN de `mudi-imsd` mediante SIP/UDP, SIP/TCP y SIP/TLS (medios SRTP). |

### Rutas de datos

```
  SIM (in the modem) ←── AT+CSIM (APDU) ──────────── mudi-imsd
                                                           │
  RG650V modem ───── AT via the GL ubus proxy ────────── mudi-imsd
        │
        ├── rmnet_data0   ← internet PDN (cid 1, default APN)
        │
        └── rmnet_data1/2 ← IMS PDN (cid 2, APN "ims")
                │                 [data call: mudi-dsi-helper, 2.2]
                │
                ├── IPsec ESP (kernel XFRM)     ← hmac-md5-96 / null
                │        │
                │    SIP/TCP → P-CSCF:5060 (initial) / port-s (protected)
                │
                └── RTP/UDP → operator media gateway
                         (SO_BINDTODEVICE to the IMS interface — §88)

  br-lan (192.168.8.1/24)
        │
        ├── SIP/UDP + SIP/TCP + SIP/TLS ← phones (Linphone on Pixel, iPhone)
        │        │ (LAN registrar + B2BUA in mudi-imsd)
        └── RTP or SRTP/UDP ← phones (ports 40000–40199)
```

### Modos de operación del módem (tres estados)

| Parámetro | Estado de fábrica del Mudi | Voz integrada del módem | External VoLTE Stack (mudi-volte) |
|----------|--------------------------|-------------------------|----------------------------------|
| `AT+QCFG="ims"` | 0 (según el perfil MBN) | 1 (habilitación forzada) | 2 (deshabilitación forzada) |
| `AT+QCALLCFG="voice_disable"` | 3 (voz apagada) | 2 | 3 |
| `voice_domain` / `ue_usage_setting` | 3 / 0 | 3 / 0 | 1 / 1 |
| `AT+CGACT` para el cid IMS | — | 1 (retenido por el módem) | 0 hasta que el host levanta el PDN |
| PDN IMS en el host | no | no | `rmnet_dataN` |
| Voz | no: el router es solo de datos | el módem maneja las llamadas por sí mismo; sin audio en el router | daemon `mudi-imsd` → teléfonos en Wi-Fi |
| SMS | módem (página SMS en el panel de administración de GL) | módem | en la red probada todavía se entregan al módem vía SGs; la ruta SMS sobre IP está implementada pero no confirmada en vivo (§88) |

`mudi-volte setup` guarda los valores originales en `/etc/mudi-volte/modem.orig`, y la reversión (rollback)
restaura exactamente esos valores, no los "de fábrica".

---

## 2. Propietario de Quectel — External VoLTE Stack

### 2.1 Cambio al modo de stack externo

Documentado en "Quectel RG520N/RM5x0N External VoLTE Stack Application Note V1.0" (2023-12-21).
Todos los comandos pasan por el proxy AT de GL con SUB1 seleccionado explícitamente (§69, §71).

#### Procedimiento completo de habilitación

```
# 1. Seleccionar SUB1 y escribir en NV
AT+QUIMSUB=1;+QCFG="ims",2
# Releer: se espera +QCFG: "ims",2  (segundo campo = capacidad VoLTE = aún 0/1)

# 2. La voz propia del módem está apagada (3 es el valor de fábrica; 2 habilita la voz integrada)
AT+QUIMSUB=1;+QCALLCFG="voice_disable",3

# 3. Voz solo sobre PS/IMS (sin respaldo (fallback) a CS)
AT+QUIMSUB=1;+QNWPREFCFG="voice_domain",1

# 4. UE centrado en datos — la red no lleva el módem a 3G para la voz
AT+QUIMSUB=1;+QNWPREFCFG="ue_usage_setting",1

# 5. Guardar NV en EFS (GL lo hace por sí mismo después de cada escritura de configuración)
AT+QEFSSYNC=0

# 6. Reiniciar el módem (= reiniciar el router en el Mudi)
AT+CFUN=1,1
# El router vuelve en ~128–180 s
```

**⚠️ Importante (§71):** sin `AT+QEFSSYNC=0` antes de `CFUN=1,1`, las escrituras en NV no sobreviven
a un reinicio forzado — ambos ajustes (`ims` y `voice_disable`) vuelven a sus valores anteriores.
El propio GL envía `QEFSSYNC=0` después de cada escritura UCI; la ingeniería inversa de `libcm_modem.so` lo confirmó.
El manual AT del RG650V (sección 1.10) da la razón: los ajustes NV modificados por un comando AT se guardan
en memoria no volátil después de 90 segundos, y un corte de energía antes de eso puede perderlos (2.9).

Verificación después del arranque:
```
AT+QUIMSUB=1;+QCFG="ims"       → +QCFG: "ims",2,0   (force disable, VoLTE cap off)
AT+QUIMSUB=1;+QIMSCFG="ims_enable"  → +QIMSCFG: "ims_enable",2,0,0  (disabled)
AT+CGACT?                       → +CGACT: 1,1  2,0   (cid2 not in use)
```

#### Reversión

`mudi-volte modem builtin` y `mudi-volte uninstall` restauran los valores guardados durante
el setup en `/etc/mudi-volte/modem.orig`, y lo mismo hace el `prerm` del paquete cuando opkg elimina
el paquete (`opkg remove`, LuCI > System > Software), solo que sin el reinicio. Para un
router que usaba la voz integrada del módem antes de la instalación:

```
AT+QUIMSUB=1;+QCFG="ims",1
AT+QUIMSUB=1;+QCALLCFG="voice_disable",2
AT+QUIMSUB=1;+QNWPREFCFG="voice_domain",3
AT+QUIMSUB=1;+QNWPREFCFG="ue_usage_setting",0
AT+QEFSSYNC=0
AT+CFUN=1,1
```

Verificación después del arranque: `QCFG ims 1,1`, `ims_enable 1,0,1`, `CGACT 1,1 2,1`.
Para un router en estado de fábrica los valores originales son distintos: `ims` 0, `voice_disable` 3.

Los valores toman efecto cuando el módem se reinicia, por lo que el servicio sigue funcionando mientras se
escriben. Una vez que el módem los aceptó todos, se consolidan de una vez con `AT+QEFSSYNC=0`:
después de una eliminación por opkg el usuario reinicia el router, desde LuCI o con su botón de encendido,
quizás en cuestión de segundos, antes de los 90 s tras los cuales el módem los guardaría por sí mismo. Un módem que no responde, o que rechaza uno de ellos, detiene la restauración: los valores
escritos hasta ese momento se devuelven a los de External VoLTE, y el servicio, el paquete, los
valores guardados y los ajustes de los teléfonos se mantienen para un reintento. El servicio se apaga
y el paquete se elimina solo después de que el módem confirmó cada valor. Un `prerm` fallido hace que
opkg conserve el paquete; `opkg remove --force-remove` lo elimina de todos modos y deja el módem en
modo External VoLTE, con `modem.orig` y los ajustes conservados para una restauración posterior.

opkg (opkg-lede) ejecuta `prerm remove` y `postrm remove` también para
`opkg install --force-reinstall`, que elimina el paquete antes de instalarlo de nuevo;
`PKG_UPGRADE` es 0 en ese caso, como en una eliminación. Una actualización o un downgrade ejecutan `prerm upgrade` y
`postrm upgrade` en su lugar. Una reinstalación conserva el módem y los datos: `install.sh` marca su
reinstalación con `MUDI_VOLTE_REINSTALL=1`, y `prerm` también encuentra `--force-reinstall` en la
línea de comandos del opkg que lo ejecuta. `mudi-volte uninstall` marca su propio `opkg remove`
con `MUDI_VOLTE_UNINSTALL=1`: ya restauró el módem y elimina los datos
por sí mismo.

`mudi-volte setup` funciona igual en la dirección opuesta. Lee cada valor antes de
escribirlo (`ims`, `voice_disable`, `ue_usage_setting`, `voice_domain`, y `mode_pref` con
`--lte-only`) y continúa solo cuando el módem los aceptó todos: sin `voice_domain` 1
la red puede mover una llamada a 3G, donde un módem en modo External VoLTE no tiene voz. Cuando el
módem rechaza un valor o no responde, setup escribe de vuelta los valores que leyó, del más reciente al más antiguo,
y falla. Desactiva el modo de suspensión del router (3.3), habilita el servicio y reinicia el
router solo después de que el módem aceptó cada valor, de modo que un setup cancelado o fallido deja el
módem, el servicio y los ajustes de suspensión como estaban. Lo que queda es la cuenta del teléfono y
el código de país, que setup crea antes de preguntar, y `modem.orig` cuando el módem tampoco
aceptó de vuelta los valores antiguos: `mudi-volte modem builtin` restaura a partir de él.

Las páginas web ejecutan setup y la restauración en segundo plano (`--detach`): una solicitud no debe esperar
ni a un módem que no responde (con sus reintentos, una consulta AT tarda minutos en ese caso)
ni al reinicio, y un servidor web que corta una solicitud larga detendría la ejecución entre dos
escrituras. La ejecución escribe su salida en `/tmp/mudi-volte-setup.log`, y su avance en
`/tmp/mudi-volte-setup.state`: `running` (luego `running PID`), `restarting` justo antes del
reinicio, `exit CODE` al final (0 en caso de éxito). Un proceso envoltorio (`mudi-volte detached`)
escribe el final, también cuando la ejecución es terminada (kill). Las páginas consultan ambos archivos cada 3 segundos: una
falla muestra la última línea de error del log, y solo `restarting` hace que anuncien el
reinicio. `--detach` se niega a iniciar una segunda ejecución mientras el PID de la primera siga vivo.

#### Parámetros de QCFG "ims"

| Valor | Significado |
|----------|-------|
| 0 | Automático, según el perfil MBN |
| 1 | Habilitación forzada (el IMS integrado del módem está encendido) |
| 2 | Deshabilitación forzada (External VoLTE Stack) |

El segundo campo de la respuesta (`0` o `1`) = con capacidad VoLTE en la red; después de cambiar al modo 2
es siempre `0`.

#### Parámetros de QCALLCFG "voice_disable"

| Valor | Significado (verificado en RG650V, registro §2) |
|----------|-------|
| 3 | Fábrica: la voz propia del módem está apagada. External VoLTE Stack necesita este mismo valor |
| 2 | La voz integrada del módem está encendida: después de un reinicio completo el módem se registra en IMS por sí mismo |
| 0 | La voz sigue apagada (`ql_voice_dial` → `-1002`) |

#### voice_domain y ue_usage_setting

`AT+QNWPREFCFG="voice_domain",1` — "solo voz PS/IMS": la red no intenta entregar
llamadas entrantes vía CS y no mueve el módem a 3G para una llamada.  
`AT+QNWPREFCFG="ue_usage_setting",1` — "centrado en datos": el módem le indica a la red que no
maneja llamadas CS; se mantiene en 4G para datos donde la voz solo está disponible en 3G. (§90)

Los valores de fábrica anteriores: `voice_domain 3`, `ue_usage 0`. Se guardan en
`/etc/mudi-volte/modem.orig` para la reversión. Todos los valores de ambos parámetros, según el manual, están en 2.9.

### 2.2 PDN IMS: una llamada de datos a través de DSI

El PDN IMS tiene que levantarse a través del propio stack de datos del módem (Qualcomm DSI), para que
netmgrd configure la interfaz `rmnet_dataN` por la que luego se comunica el daemon. El firmware tiene una
biblioteca para eso, `/usr/lib/libdsi_netctrl.so`, y no tiene headers para ella; el único
cliente de la biblioteca en el router es la herramienta de prueba interactiva `dsi_netctrl_test`. MudiVoLTE llama a la
biblioteca directamente mediante `mudi-dsi-helper`; no hay respaldo basado en menús ni selector de backend.
El cliente en C está en `src/dsi-helper/mudi-dsi-helper.c`, su supervisor en
`src/imsd/datacall.go`.

El helper mantiene una llamada. El daemon lo mantiene en ejecución mientras quiera el PDN,
y toma su término, o `DSI_EVT_NET_NO_NET` después de la conexión, como la pérdida del PDN (§81, §87).
En el firmware 4.10.0 el helper alcanzó `DSI_EVT_NET_IS_CONN` en 0,6–1,1 s, frente a unos
2,6 s del antiguo cliente de menús (§131). Estos son tiempos de llamada de datos, no tiempos de registro IMS.

#### El helper: mudi-dsi-helper

```
mudi-dsi-helper SUBSCRIPTION PROFILE FAMILY
mudi-dsi-helper check
```

`SUBSCRIPTION` es `1` o `2` (`0`: la suscripción que sea la predeterminada para datos), `PROFILE`
es el número del perfil del módem (1 a 255; ver "Cómo encontrar el número de perfil IMS" más abajo),
`FAMILY` es `ipv4v6` o `ipv4`. El daemon ejecuta `mudi-dsi-helper 1 <profile> ipv4v6` y, cuando
esa llamada no se levanta, `mudi-dsi-helper 1 <profile> ipv4`.

El helper hace las llamadas que hace `dsi_netctrl_test` para crear e iniciar una llamada legacy, con los
mismos argumentos, en el mismo orden. Se obtuvieron leyendo el código de la herramienta y de la biblioteca
(§131); los nombres son los del `dsi_netctrl.h` de Qualcomm.

| # | Llamada | Argumentos | Notas |
|---|---|---|---|
| 1 | `dsi_set_modem_subs_id(subs)` | `1` | No se hace para la suscripción `0`. Sin ella la llamada se levanta en la SIM que lleva internet (la suscripción DDS), que GL puede mover a la otra SIM (§117, §118) |
| 2 | `dsi_init(mode)` | `0` (`DSI_MODE_GENERAL`) | Retorna antes de que la biblioteca haya tenido noticias de netmgrd |
| 3 | `dsi_get_data_srvc_hndl(callback, user_data)` | | Retorna `NULL` hasta que la biblioteca esté lista. El helper vuelve a pedirlo cada 200 ms, durante hasta 15 s; en el dispositivo la segunda solicitud obtiene el handle |
| 4 | `dsi_set_data_call_param(handle, 11, &value)` | `DSI_CALL_INFO_IP_VERSION`; `value.num_val` es `10` (IPv4v6) o `4` (IPv4) | 0 = aceptado |
| 5 | `dsi_set_data_call_param(handle, 1, &value)` | `DSI_CALL_INFO_UMTS_PROFILE_IDX`; `value.num_val` es el número de perfil | 0 = aceptado |
| 6 | `dsi_start_data_call(handle)` | | 0 = aceptado; el resultado llega como un evento |
| 7 | `callback(handle, user_data, event, payload)` | `1` = `DSI_EVT_NET_IS_CONN`, `2` = `DSI_EVT_NET_NO_NET` | Corre en un hilo de la biblioteca; el helper solo pasa el número de evento a su bucle principal |
| 8 | `dsi_stop_data_call(handle)` | | Para terminar la llamada. El helper luego espera hasta 2 s por `DSI_EVT_NET_NO_NET` |
| 9 | `dsi_rel_data_srvc_hndl(handle)` | | |

`value` es un `dsi_call_param_value_t`: `struct { char *buf_val; int num_val; }`, 16 bytes en
arm64; `buf_val` no se lee para estos dos parámetros.

Dos cosas que hace la herramienta se omiten a propósito:

- Para "AUTOMATIC" la herramienta también fija una preferencia de tecnología:
  `dsi_set_data_call_param(handle, 9, &value)` (`DSI_CALL_INFO_TECH_PREF`) con `num_val` 0.
  La biblioteca rechaza ese valor (retorna -1) y no cambia nada, y la herramienta no revisa
  el resultado. "Automático" es una preferencia que nadie fijó, así que el helper no fija ninguna.
- El helper termina con `_exit` y nunca ejecuta los manejadores de salida. La biblioteca registra uno que desarma
  su estado bajo locks que sus propios hilos pueden tener tomados. La herramienta tampoco lo ejecuta nunca: se
  termina mediante una señal. El kernel cierra los sockets QMI, y el módem libera lo que tenía el handle.

**Carga de la biblioteca.** El helper carga `/usr/lib/libdsi_netctrl.so` con
`dlopen(RTLD_NOW | RTLD_GLOBAL)` y resuelve las siete funciones anteriores con `dlsym`.
No hay lista de hashes permitidos de la biblioteca: una recompilación de la misma API no requiere un helper nuevo.
`mudi-dsi-helper check` carga la biblioteca y resuelve las funciones sin llamarlas ni
establecer una llamada de datos. Si tiene éxito imprime estas dos líneas y sale con 0:

```
{"v":1,"event":"library"}
{"v":1,"event":"exiting","code":0}
```

Una biblioteca o dependencia faltante produce `library_load`; una función faltante produce
`library_symbol`. Ambos salen con 1 y dejan el diagnóstico del loader en stderr. Los
constructores de la biblioteca también escriben en stderr, no en el flujo de eventos. En modo `check` el helper deja las
señales de detención como las encontró (por defecto lo terminan de inmediato), de modo que incluso un loader colgado puede
terminarse sin esperar a que una llamada se detenga, y una biblioteca que no se cargó en 10 s
cuenta como una que no carga (`library_load`): `mudi-volte setup` espera esta respuesta.

Las declaraciones y valores se verificaron contra el firmware 4.10.0 y el `dsi_netctrl.h` de Qualcomm.
La búsqueda de símbolos no es una prueba de versión de ABI: un firmware que cambie una firma
o la disposición de una estructura requiere verificación y posiblemente declaraciones actualizadas. El helper revisa cada
código de retorno de la API e informa el resultado real de la conexión; otras versiones de firmware todavía no
se han validado en un dispositivo.

**Eventos.** Un objeto JSON por línea en stdout; `"v":1` es la versión de este protocolo.
Lo que impriman las bibliotecas va a stderr: el helper escribe sus eventos en un duplicado de su
stdout y apunta el descriptor 1 a stderr antes de cargar la biblioteca, para que lo que imprima una biblioteca
no pueda caer dentro de una línea de evento.

| Evento | Significado | El daemon |
|---|---|---|
| `{"v":1,"event":"library"}` | La biblioteca está cargada y tiene las funciones: la respuesta a `check`, y para una llamada el primer evento, antes del paso 1 | de aquí en adelante una falla es del router, no de la instalación |
| `{"v":1,"event":"ready","pid":N,"subscription":1,"profile":2,"family":"ipv4v6","tries":2}` | La llamada está configurada (pasos 1–5); se inicia ahora. `tries` cuenta las solicitudes del paso 3 | lo registra en el log; de aquí en adelante la red tiene la palabra |
| `{"v":1,"event":"net_up"}` | `DSI_EVT_NET_IS_CONN`: la llamada está conectada | continúa con 2.3 |
| `{"v":1,"event":"stopping","signal":N}` | Se le pidió al helper que se detuviera y detiene la llamada ahora (paso 8): por la señal `N`, o por el fin de su stdin cuando `N` es 0. El `net_down` que sigue es esta detención | cuando el daemon no lo pidió: la llamada se perdió en el router, no en la red |
| `{"v":1,"event":"net_down"}` | `DSI_EVT_NET_NO_NET`: la red rechazó la llamada, o la terminó | antes de `net_up`: prueba la siguiente familia IP; después: el PDN se perdió |
| `{"v":1,"event":"dsi_event","code":N}` | Cualquier otro evento de la biblioteca, por su número. En el firmware 4.10.0, 19 llega justo antes de `net_up` y 20 justo antes de `net_down` | lo registra en el log |
| `{"v":1,"event":"error","stage":"NAME","code":N}` | Un paso falló. `NAME` es la función de la biblioteca que falló (`dsi_set_data_call_param` agrega `"param":N`), o `library_load`, `library_symbol` para una biblioteca que el helper no puede usar, o `setup` para una falla propia | la llamada falló; de quién es la culpa se deduce de los eventos anteriores (ver "Manejo de fallas") |
| `{"v":1,"event":"exiting","code":N}` | La última línea; `N` es el código de salida | |

Código de salida: 0 = detenido a petición, 1 = un paso falló, 2 = uso, 3 = la red terminó la llamada.

**Detención.** SIGTERM (o SIGINT, SIGHUP), o el fin de su stdin: el helper informa
`stopping`, detiene la llamada (pasos 8–9) y sale. El daemon mantiene un pipe hacia el stdin del helper
y lo cierra para detener la llamada. Un daemon que muere también cierra ese pipe, de modo que la llamada no
sobrevive al daemon; un helper que no encuentra a nadie leyendo su `ready` no inicia la llamada en absoluto.
Con el daemon ausente, nadie lee tampoco los eventos ni el stderr del helper. El helper retiene SIGPIPE
mientras escribe una línea propia, y una vez que una línea no encontró lector apunta stdout y
stderr a `/dev/null`: ni él ni un hilo de la biblioteca mueren a medio camino de la detención.
Medido en el dispositivo (§131):

- stdin cerrado, o SIGTERM: el helper ha salido y la dirección IMS desaparece en menos de 0,1 s;
- `kill -9` del daemon: su helper desaparece en menos de 0,1 s;
- `kill -9` del helper: el módem libera la llamada, la dirección IMS desaparece en menos de 0,3 s; el
  PDN de internet sigue arriba. El daemon lo nota de inmediato, establece la llamada de nuevo y se registra (17 s
  desde el kill hasta el registro, 5 s de ellos una espera por la respuesta a un de-REGISTER que el
  daemon ya no envía, ver 4.1; la radio no se reinicia);
- un segundo helper para el mismo perfil obtiene `net_up` para la llamada que ya está arriba, y la
  llamada sigue arriba cuando el segundo se va: el primero todavía la mantiene.

#### Manejo de fallas

De quién es la culpa de una llamada fallida se deduce de hasta dónde llegó el helper, no del nombre del
paso que falló (`blame` en `src/imsd/datacall.go`):

| El helper llegó | Una falla significa | La sesión termina con | Reinicio de radio |
|---|---|---|---|
| a ninguna parte: ausente, no se puede ejecutar, sin evento `library` (`library_load`, `library_symbol`, `setup`, una salida silenciosa, un cuelgue durante la carga, otra versión de protocolo) | el helper no puede funcionar en esta instalación; no se prueba la otra familia IP | `helper-unusable` | nunca |
| más allá de `library`, sin llegar a `ready` (`dsi_init`, sin handle, un parámetro rechazado, un crash o cuelgue en la biblioteca) | la llamada falló en el router; se prueba la otra familia IP | `helper-failed` | nunca |
| más allá de `ready`, con noticias sobre la llamada (`dsi_start_data_call` falló, `net_down`, sin respuesta) | la red tuvo la palabra; se prueba la otra familia IP | `pdn-failed` | en la segunda consecutiva |
| más allá de `ready`, antes o después de `net_up`, también mientras el daemon espera la dirección después de él: un `stopping` que el daemon no pidió, o una salida sin noticias sobre la llamada | algo en el router terminó la llamada: una señal, `kill`, un crash | `helper-failed` | nunca |
| `net_up`, luego `net_down` | la red terminó la llamada | `pdn-lost`; `pdn-failed` mientras la dirección todavía no ha llegado | tras un paso por 3G, o en la segunda caída dentro de 15 min (§87); `pdn-failed`: en la segunda consecutiva |

Cuando ambas familias IP fallan, la sesión cuenta como `helper-failed` solo cuando ninguna llegó hasta
la red. `helper-unusable` y `helper-failed` nunca provocan un reinicio de radio, y tampoco
cuentan para uno: el primer rechazo después de ellos es un primero, no un segundo. Reiniciar la
radio no puede reparar el lado del router de la llamada e interrumpiría internet (§131).

"Consecutiva" cuenta las fallas en las que la radio o la red tuvieron la palabra (todo término excepto
`helper-unusable` y `helper-failed`) desde el último registro o reinicio de radio: cualquiera de los dos
reinicia la cuenta, y también una sesión que funcionó durante 30 minutos. Después de un reinicio de radio
se necesitan dos fallas más de ese tipo para el siguiente, nunca más de 3 reinicios por hora. Una sesión
que termina con `rat-fallback` o `registration-failed` reinicia la radio de inmediato.

El daemon prueba la siguiente sesión después de 5 s, 15 s, 30 s, 1 min, 2 min, y luego cada 5 min. Esta
espera cuenta cada sesión fallida consecutiva, registrada o no, y un reinicio de radio no
la acorta: solo una sesión que funcionó durante 30 minutos la reinicia, para que un registro que
dura segundos y vuelve a caer no haga que el daemon se registre cada 5 s. Código: `loop` en
`src/imsd/service.go`.

**Reinicios después de un crash.** procd vuelve a iniciar un daemon que terminó sin una detención 10 s después,
sin límite (`respawn 3600 10 0`), y cada proceso nuevo establecería la llamada de datos y
se registraría de nuevo. `/var/run/mudi-volte/crashes.json` (tmpfs, junto al archivo de estado) le indica al siguiente
proceso si el último terminó por decisión propia (una detención, o un error que informó, como un
`ims_profile` incorrecto) o no (un panic, un kill, el OOM killer), y cuántos seguidos no lo hicieron. El
proceso que sigue a tales términos espera antes de su primera llamada de datos: nada después del primero,
luego 10 s, 30 s, 1 min, 2 min, y 5 min a partir del sexto consecutivo, más los 10 s de procd.
Mientras tanto el servidor SIP de los teléfonos está arriba; sus llamadas reciben 503. La cuenta se reinicia después de una
detención limpia (`/etc/init.d/mudi-volte stop` o `restart`, `uci commit mudi-volte`, una actualización), después de
30 minutos en ejecución y con un reinicio del equipo. El archivo de estado lo muestra como `crash_restarts` (0 de nuevo
después de los 30 minutos) y la espera como `last_error`; `mudi-volte status` imprime "Restarts of the
service after a crash: N in a row". Código: `src/imsd/crashes.go`.

`mudi-volte setup` verifica el helper y la API DSI antes de tocar el módem
(`check_data_call` en `package/files/usr/sbin/mudi-volte`). No existe una opción `data_call`;
la clave de configuración opcional `dsi_helper` del daemon reemplaza la ruta del ejecutable del helper.
La antigua clave JSON `dsi_path` se ignora al actualizar. Un `ims_profile` fuera de 0–255 termina el
servicio al iniciar, con el error en `mudi-volte status`: es un error en la configuración,
no una falla de la llamada (procd vuelve a iniciar el servicio cada 10 s, con el mismo resultado; ese
término no cuenta como crash, no llega a ninguna red).

En una llamada exitosa, `dsi event=net_up` precede al descubrimiento de la dirección y al registro.

Cuando el servicio inicia, `killStaleDSI` (`src/imsd/datacall.go`) detiene los procesos remanentes de
`mudi-dsi-helper` o `dsi_netctrl_test`, y los procesos del helper que indique `dsi_helper`
(identificados por su nombre y por el archivo que ejecutan, `/proc/PID/exe`). `dsi_netctrl_test` es
limpieza para versiones antiguas y pruebas manuales, no una forma de iniciar una llamada nueva. El módem comparte una
llamada entre los clientes del mismo perfil, de modo que un cliente remanente mantendría vivo el PDN después de que el
daemon detuviera su propia llamada.

#### Cómo encontrar el número de perfil IMS

```
AT+CGDCONT?
# Buscar la línea con APN "ims" → tomar el primer campo (cid, normalmente 2)
```

Código: `parseIMSProfile` en `src/imsd/pdn.go`.

### 2.3 AT+CGCONTRDP — parámetros del PDN IMS

Después de `DSI_EVT_NET_IS_CONN`, leer los parámetros:

```
AT+CGCONTRDP=2
```

Ejemplo de una respuesta en vivo (§75; las direcciones DNS públicas se reemplazan por direcciones de documentación):
```
+CGCONTRDP: 2,6,"ims","198.51.100.20",,"203.0.113.105","203.0.113.106","192.0.2.20","192.0.2.28"
```

Campos según TS 27.007 §10.1.23:
1. `cid` = 2
2. `bearer_id` = 6
3. APN = "ims"
4. dirección IP del PDN (posiblemente con máscara; el campo de máscara separado está vacío)
5. gateway (vacío — ninguno)
6. DNS1
7. DNS2
8. **P-CSCF 1** ← primero
9. **P-CSCF 2** ← segundo

**⚠️ Trampa (§75):** el gateway está vacío, y los P-CSCF vienen DESPUÉS de las dos direcciones DNS — el parser debe
contar por posición, no por contenido.
Código: `parseCGCONTRDP` en `src/imsd/pdn.go` (campos 8 y 9 = P-CSCF).

Interfaz IMS: `rmnet_data*` con la dirección del campo 4 de CGCONTRDP.  
**⚠️ Trampa (§87):** los números `rmnet_dataN` del PDN de internet y del PDN IMS pueden intercambiarse
después de una reconexión (por ejemplo, `rmnet_data1` ← internet después de CFUN 4→1).
Código: `findIMSIface` en `src/imsd/pdn.go` — busca la interfaz con la IP de CGCONTRDP, no
un nombre fijo.

### 2.4 AT+QIMSCFG="authenticate"

El comando que ejecuta AKA en la SIM (§72):

```
AT+QUIMSUB=1
AT+QIMSCFG="authenticate",11,"10<RAND_32hex>10<AUTN_32hex>"
```

- Contexto `11` = IMS AKA.
- `RAND` y `AUTN` se pasan como cadena hexadecimal, cada uno con el prefijo `10` (longitud 16 bytes = 0x10).
- Respuesta en caso de éxito: `+QIMSCFG: "authenticate",DB,<RES_hex>,<IK_hex>,<CK_hex>`
- Respuesta en caso de falla de sincronización: `+QIMSCFG: "authenticate",DC,<AUTS_hex>`
- `AUTS` se usa para resincronizar el SQN (un REGISTER repetido con `auts=`).

**Limitaciones (§72):**
- `QIMSCFG` (`authenticate` y otros) NO funciona en una línea compuesta después de `QUIMSUB=1;`
  (ERROR). Requiere una llamada separada `AT+QUIMSUB=1`, y luego el comando inmediatamente después.
- Una llamada repetida con el mismo AUTN → `DC` (la SIM consume el SQN; una repetición es una reacción normal
  ante un reintento de transacción, pero la SIM la rechazará).
- **Nunca reintentar una respuesta AKA exitosa** (§72, §74 — protección en `atproxy.go`).

**La ruta de trabajo del daemon es AT+CSIM (§74, §88):** `AT+CSIM` con la APDU AUTHENTICATE
(`00 88 00 81 ...`). Es un comando SIM estándar y no necesita `QIMSCFG`;
`QIMSCFG "authenticate"` se usó solo en las primeras pruebas manuales (§72).
Implementación: `src/imsd/csim.go` (transporte); la respuesta de la tarjeta la interpreta la biblioteca wwan-go.

### 2.5 QIMSCFG para leer identidades

Legibles una vez que la SIM informa `+CPIN: READY`, tanto con el IMS integrado del módem como en modo External VoLTE (§71):

```
AT+QUIMSUB=1
AT+QIMSCFG="impi"     → IMPI (IMS Private Identity = <IMSI>@ims.mncXXX.mccYYY.3gppnetwork.org)
AT+QIMSCFG="impu"     → IMPU (sip:<IMSI>@ims.mncXXX...)
AT+QIMSCFG="domain"   → IMS home domain
AT+QIMSCFG="user_agent"
```

Sin ISIM, IMPI/IMPU se derivan del IMSI según TS 23.003 §13. Si la SIM tiene ISIM,
`sim.Card` de wwan-go lee las identidades ISIM reales mediante `AT+CSIM` (§88).

### 2.6 AT+QENG — información de celda para P-Access-Network-Info

```
AT+QENG="servingcell"
```

El formato de la respuesta varía (§77, §85):

- Una sola línea: `+QENG: "servingcell","NOCONN","LTE","FDD",MCC,MNC,cellID,PCID,EARFCN,band,ULbw,DLbw,TAC,...`
- Varias líneas (EN-DC, LTE+NR5G-NSA):
  ```
  +QENG: "servingcell","NOCONN"
  +QENG: "LTE","FDD",MCC,MNC,cellID,PCID,EARFCN,band,ULbw,DLbw,TAC,...
  +QENG: "NR5G-NSA",...
  ```

**⚠️ Trampa (§85):** después de la palabra `"LTE"` los campos son, en orden:
`duplex, MCC, MNC, cellID, PCID, EARFCN, band, ULbw, DLbw, **TAC (+10)**`.
TAC es el **décimo** campo, no el sexto (ese es EARFCN). El parser debe contar desde el token `"LTE"`.
Código: `parseQENGANI` en `src/imsd/pdn.go`, con 3 reintentos y la respuesta cruda en el error.

Formato de P-Access-Network-Info (TS 24.229 §7.2A.4):
```
3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=<PLMN><TAC4hex><ECI7hex>
# ejemplo (celda ficticia): 3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=001013C0D01A2B03
# PLMN=00101 (MCC=001, MNC=01: la red de prueba 3GPP), TAC=3C0D, ECI=01A2B03
```

Si el módem está en WCDMA (un estado transitorio después de que se levanta el PDN), no construir un PANI para E-UTRAN;
esperar LTE (§79).

El ID de celda revela la ubicación del router con una precisión de cientos de metros. Desde la 0.9.14 el daemon
registra en el log solo el PLMN (`…=00101…`) en nivel info; el valor completo se registra en nivel debug.
Las celdas de los ejemplos de este documento y de las pruebas son ficticias.

### 2.7 AT+CGSN — IMEI

```
AT+CGSN
```

Retorna el IMEI de 15 dígitos. Se usa en `+sip.instance` (RFC 7254: `urn:gsma:imei:...`).

### 2.8 AT+COPS / AT+CEREG

```
AT+COPS?   → MCC, MNC, current operator
AT+CEREG?  → LTE registration status (0,1 = registered home; 0,5 = registered roaming)
```

Se espera `CEREG: 0,1` o `0,5` después de `CFUN=1,1` y después de `CFUN 4→1`.
Buscar `"L"` en la respuesta no funciona porque el proxy ubus escapa las comillas (§87).

### 2.9 Contraste con el manual AT del RG650V

Los comandos de 2.1–2.8 se tomaron de documentos de la familia vecina RG520N/RM5x0N y de la experiencia.
"RG65xE&RG650V&RM550V&RM551E Series AT Commands Manual" V1.0 (Released, 2024-07-11) es
el manual de la propia familia del módulo; se obtuvo el 2026-11-05. Confirma los comandos comunes y no describe
ninguno de los comandos del modo de stack externo. Los números de página son los impresos en el pie de página del manual.

| Comando | Sección, página | Lo que dice el manual |
|---------|--------------|-----------------------|
| `AT+QNWPREFCFG="voice_domain"` | 5.22.7, p. 134 | 0 — solo CS; 1 — solo IMS PS; 2 — CS, IMS PS como respaldo; 3 — IMS PS, CS como respaldo. Toma efecto de inmediato; se guarda automáticamente, con el retardo de 1.10 |
| `AT+QNWPREFCFG="ue_usage_setting"` | 5.22.9, p. 136 | 0 — centrado en voz; 1 — centrado en datos. Toma efecto de inmediato; se guarda automáticamente, con el retardo de 1.10 |
| `AT+QEFSSYNC` | 3.5, p. 54 | `=0` — guarda NV de forma síncrona, `OK` después de la escritura (más de 300 ms); `=1` — de forma asíncrona, `OK` de inmediato |
| Retardo de escritura NV | 1.10, p. 14 | En módulos con eMMC, los ajustes NV modificados por un comando AT se guardan 90 s después del comando; un corte de energía antes de eso puede perderlos. Guardar los ajustes importantes con `AT+QEFSSYNC`, una vez después de todas las escrituras |
| `AT+QENG="servingcell"` | 5.18, p. 102 | Formato de respuesta para LTE, NR5G-SA y EN-DC |
| `AT+QUIMSLOT` | 4.15, p. 74 | Cambio de slot SIM |
| `AT+CGSN`, `AT+CFUN`, `AT+CIMI`, `AT+CSIM`, `AT+COPS`, `AT+CREG`, `AT+CEREG`, `AT+CGDCONT`, `AT+CLCC`, `AT+CGACT` | pp. 20, 29, 55, 63, 75, 77, 81, 84, 159, 207 | Descritos |

Los comandos SMS de 3GPP TS 27.005 también están en el manual (capítulo 9, pp. 178–204; `AT+CGSMS` — 10.6, p. 213).
El proyecto no los usa; para SMS ver 4.4.

Los comandos de los que depende el modo de stack externo no están en el manual. Verificado mediante una búsqueda de texto
en el manual y en los demás documentos recopilados para el proyecto:

| Comando | Dónde está descrito |
|---------|-------------|
| `AT+QCFG="ims"` | External VoLTE Stack Application Note (RG520N). En el manual del RG650V la sección `AT+QCFG` (3.3, p. 37) describe 12 subcomandos, y `"ims"` no es uno de ellos |
| `AT+QIMSCFG` | Solo la External VoLTE Stack Application Note |
| `AT+CGCONTRDP` | 3GPP TS 27.007 §10.1.23 y la misma Application Note |
| `AT+QCALLCFG="voice_disable"` | Ninguno de los documentos; los valores se establecieron por experimentación (2.1) |
| `AT+QUIMSUB` | Ninguno de los documentos; el comportamiento se estableció por experimentación (3.1, §69) |

---

## 3. Propietario de GL.iNet — firmware del Mudi

### 3.1 Proxy AT vía ubus

El GL.iNet Mudi usa un proxy ubus para los comandos AT en lugar del acceso directo a `/dev/smdN`:

```sh
ubus call modem.CPU.AT get_result_AT \
  '{"cmd":"AT+QCFG=\"ims\"","timeout":12,"sub_id":1}'
```

Respuesta JSON: el campo `data` = las líneas de la respuesta AT (CR/LF).

Código en el daemon: `ubusAT` en `src/imsd/atproxy.go`; en el script: `at_raw()` en `package/files/usr/sbin/mudi-volte`.

#### Trampa de SUB2 (§69) — LECTURA OBLIGATORIA

El poller en segundo plano de GL (`libcm_modem.so`, función `quectel_set_slot_sub_id`) cambia
constantemente el contexto del puerto AT entre SUB1 (el slot con la SIM) y SUB2 (vacío). Una sola
llamada con `sub_id:0` puede caer en el contexto del SUB2 vacío.

Con `sub_id` 1 o 2 el proxy envía `AT+QUIMSUB=<n>` por sí mismo, a menos que el slot que recuerda ya
sea ese. No ve un `AT+QUIMSUB` dentro de una línea de comando, de modo que una línea así lo deja
recordando el slot equivocado, y la propia solicitud de GL para el otro slot puede ser respondida desde el nuestro
(§117). El daemon y el script llaman al proxy con `sub_id:1` y aun así ponen la selección
en la línea AT.

**Síntoma:** `QCFG ims` → `0,0` en lugar de `1,1` / `2,0`; `ims_enable` → `0,1,0`.

**Regla:** toda línea AT comienza con `AT+QUIMSUB=1;` (esto fija el slot). Para consultas
compuestas:
```
AT+QUIMSUB=1;+QUIMSUB?;+QCFG="ims"
```
La respuesta debe contener `+QUIMSUB: 1` — esto confirma que el comando se ejecutó en SUB1.

**Limitación (§72, §74):** `AT+QIMSCFG="authenticate"`, `AT+QIMSCFG="impu"` y los demás
subcomandos de `QIMSCFG` NO funcionan en una línea compuesta (`AT+QUIMSUB=1;+QIMSCFG=...` →
ERROR). Para ellos: un `AT+QUIMSUB=1` separado, y luego `AT+QIMSCFG=...` inmediatamente después.

**`AT+CSIM` va en una sola línea con la selección (`ATLink.csim`, §117):** con una SIM activa en el
otro slot, una APDU enviada ahí recibe una respuesta normal, y nada indica que llegó a la
tarjeta equivocada. Medido con dos SIM (§118): con selección y comando como dos llamadas al proxy, 7 de 120
respuestas vinieron de la otra SIM; en una sola línea, ninguna de 60.

**Condición de carrera en modo servicio (§90):** si dos procesos seleccionan SUB1 al mismo tiempo (el daemon +
`mudi-volte status`), uno de ellos pierde el contexto. Solución: las primitivas AT se serializan mediante
un mutex en `ATLink`; el script `mudi-volte` tiene un `at()` separado con reintentos.

#### Reintentos de ATLink

`ATLink.command()` / `ATLink.query()` / `ATLink.csim()` en `src/imsd/atproxy.go`:
- Hasta 4 intentos (`Retries: 4` en `main.go`).
- Lo que provoca un reintento: `CME ERROR`, `No Service`, `ERROR` → `wrongContext()` → reintento.
- **Excepción:** un `+CSIM:` exitoso en la respuesta nunca se reintenta (protección del SQN).

### 3.2 fw4 (nftables de OpenWrt)

La interfaz IMS (`rmnet_dataN`) no está en ninguna zona de fw4. Sin una regla explícita, el tráfico
ESP entrante desde el P-CSCF se rechaza como "nuevo" una vez que expira la entrada de conntrack para el
protocolo genérico 50 (~200 s, §87).

Regla persistente: `/usr/share/nftables.d/chain-pre/input/20-mudi-volte.nft`:

```nft
# mudi-volte: permitir que el núcleo IMS del operador alcance al cliente VoLTE.
iifname "rmnet_data*" meta l4proto esp counter accept  comment "mudi-volte: IMS ESP in"
iifname "rmnet_data*" meta ipsec exists counter accept comment "mudi-volte: IMS IPsec in"
```

Primera regla: deja pasar el ESP cifrado hacia la desencapsulación XFRM.  
Segunda regla (`meta ipsec exists`, sinónimo de `meta secpath` en nft 1.0.8+): deja pasar
el TCP/UDP descifrado desde el P-CSCF después de que el kernel lo ha desencapsulado.

`iifname "rmnet_data*"` cubre todos los números (1, 2, …); `rmnet_data0` (internet)
no tiene SA IMS, así que el ESP entrante por ella no pasará la verificación criptográfica de XFRM.

fw4 incluye `chain-pre/input/*.nft` automáticamente en cada `fw4 reload`
(reinicio de WAN, hotplug). La regla va antes de `ct state established,related accept`.

**⚠️ Trampa (§88):** la primera regla sola no basta. Una vez que el kernel ha quitado el ESP,
el TCP desde el P-CSCF hacia port-s llega de nuevo a la cadena input como un paquete plano
desde una interfaz WAN, y sin la segunda regla (`meta ipsec`) termina en el reject de fw4.

### 3.3 Particularidades del firmware del Mudi (GL-E5800)

- **No hay `setsid`** (solo `nohup`) — inicio en segundo plano: `nohup "$0" ... &` (§89, `mudi-volte detach`).
- **No hay historial de `logread`** más antiguo que ~5 min (buffer del kernel ~256 KB, muy ocupado por ql_ril).
  El daemon escribe en `/var/log/mudi-volte.log` (1 MB + rotación a `.1`). La zona horaria se toma de
  `system.@system[0].zonename`; la base de datos de zonas está integrada en el binario. El archivo está en `tmpfs` y no sobrevive a un reinicio.
- **procd** es el sistema init. El servicio `mudi-volte` está en `/etc/init.d/mudi-volte` (rc.common,
  `USE_PROCD=1`): `respawn` sin límite, `term_timeout 30`, reinicio con `uci commit mudi-volte`.
  Después de cada crash consecutivo el daemon espera más antes de su primera llamada de datos (2.2).
- **La verificación de fw4** no está disponible en algunas versiones de firmware (§89).
- **`/etc/rc.common` START=99** — el servicio inicia al último, después de la red y la LAN.
- **El overlay difiere del OpenWrt normal** (`/lib/preinit/80_mount_root`, la rama `e5800`):
  la partición eMMC `systemrw` (ext4) se monta en `/overlay`, la capa superior es
  `/overlay/rootfs-upper`, el directorio de trabajo es `/overlay/.rootfs-work`; la raíz de solo lectura
  está en `system` / `system_b` (slots de actualización A/B).
- **`firstboot -y` no borra nada aquí** (verificado 2026-11-05): `jffs2reset` espera
  una partición `rootfs_data`, que el E5800 no tiene. Después de `firstboot -y; reboot` el router
  arranca con todos sus ajustes y paquetes.
- **Restablecimiento de fábrica por el firmware de GL** (el botón Reset Firmware, RPC `reset_firmware`
  en `/usr/lib/oui-httpd/rpc/system`): `set_modem_cfun0`, luego
  `atcmd 'AT+QCFG="ResetFactory"'` (reset del módem) y el mismo `firstboot -y; reboot`.
  No hemos presionado el botón: lo que queda del paquete y de los ajustes del módem después de eso no está verificado.
- **Restablecimiento manual de los ajustes del router** (hecho 2026-11-05 11:20, confirmado): eliminar el paquete
  (`mudi-volte uninstall -y` devuelve el módem a su modo estándar), luego como root
  `rm -rf /overlay/rootfs-upper/* /overlay/rootfs-upper/.[!.]* /overlay/.rootfs-work/*`,
  `sync`, `reboot -f` — lo mismo que haría `jffs2reset` si conociera los nombres de directorio de GL.
  Después del reinicio: SSID de fábrica `GL-E5800-xxx`, SSH cerrado hasta que se haya completado el asistente de configuración web,
  la contraseña de root antigua ya no funciona, nuevas concesiones DHCP. Esta ruta no toca
  el módem (uninstall ha restaurado sus ajustes). Borrar el upperdir con el sistema en ejecución
  es aceptable solo con un reinicio inmediato, como hace el `jffs2reset` estándar.
- **El ahorro de energía de `lpm-monitor` pone a dormir el router completo** (uci `lpm.global`):
  `wifi_sleep_time` (s; sin clientes Wi-Fi → radio apagada), `phy_sleep_time` (sin enlace
  cableado); con `system_sleep_enable=1`, después de ambos — "ap sleep": módem `CFUN=0`, SoC en suspensión
  (el reloj del kernel se detiene), y después de `auto_shutdown_time` segundos de suspensión — `poweroff`. Valores de fábrica:
  900/900/1/18000. Para una línea VoLTE todo esto debe estar apagado: 0 = nunca
  (`/etc/init.d/lpm_monitor restart` después de `uci commit lpm`). `mudi-volte setup` hace esto
  por sí mismo y guarda los valores originales en `/etc/mudi-volte/lpm.orig`; uninstall los restaura (§91, 2026-11-05).
  Setup lo hace solo una vez que el módem aceptó sus valores: un setup fallido deja la suspensión como estaba.
- **Después de una actualización de firmware**, `clean_overlay` en el mismo script de preinit borra `/overlay` cuando
  `/overlay/.overlay_clr_flag` está activado, conservando solo los ajustes guardados por el firmware
  (`cfg_bak`). A juzgar por el script, los paquetes instalados no sobreviven a una actualización
  y `mudi-volte` tiene que instalarse de nuevo; esto no se ha verificado con una actualización real.
- `AT+CFUN=1,1` en el Mudi = un reinicio del router completo (~130–180 s). No existe un comando separado
  para "reiniciar solo el módem".
- **Dos SIM (§117, §118).** Dos slots nano-SIM y una eSIM integrada que ocupa el lugar de
  la SIM 2 (`AT+QSIMTYPE="slot2"`: 0 = nano-SIM, 1 = eSIM). Ambas SIM permanecen registradas (dual standby);
  internet pasa por una, y el SIM Card Switch de GL la cambia (`current_sim_slot` en
  `ubus call cellular.modem status`), sin reinicio. La línea telefónica permanece en SUB1. Con
  internet en la otra SIM el PDN IMS sigue arriba, pero el RTP de una llamada tiene cortes de 0,23–0,4 s
  aproximadamente cada 2,4 s; con internet en la SIM 1, ninguno sobre 29 ms en sentido saliente (una llamada grabada de
  cada caso). La causa probable es la radio compartida entre las SIM.

### 3.4 Pantalla de GL (gl_screen)

El daemon `gl_screen` (paquete `gl-sdk4-screen-large`, procd, ubus) dibuja la interfaz del firmware en
`/dev/fb0` (240×320 px, RGB565, panel GC9307C sobre SPI).

- Retroiluminación: `/sys/class/backlight/soc:backlight` (`bl_power 0/1`, `brightness 0–120`; GL fija 36+20).
- Pantalla táctil: `/dev/input/event0` (`chsc_cap_touch`, i2c `1-002e`, driver `semi_touch`).
- Métodos ubus: `ubus call gl_screen set '{"method":"reload|usb_detected|switch|..."}'`.
- No se pueden agregar páginas personalizadas a `gl_screen` (los archivos de diseño en `/etc/gl_screen/config`
  son propietarios).

**Dibujar un código QR (§90):** detener `gl_screen` → escribir directamente en `/dev/fb0` (RGB565) →
controlar la retroiluminación vía sysfs → iniciar `gl_screen` de nuevo.

**⚠️ Pantalla táctil mientras gl_screen está detenido (§90):** los toques se leen desde
`/dev/input/event0` solo si la pantalla no estaba dormida en ese momento. El driver de la pantalla táctil no
despierta de la suspensión: la retroiluminación se puede despertar vía sysfs, el sensor no. Por eso la pantalla con
el código QR se cierra con un temporizador (la pantalla indica después de cuántos segundos), y un toque
es solo una forma adicional de cerrarla.

---

## 4. Protocolos estándar — implementación

### 4.1 Registro IMS (sec-agree, IPsec, AKA)

El ciclo completo se verificó en vivo en la red de un operador europeo (§75).

#### Secuencia

```
UE                                  P-CSCF (192.0.2.28:5060)
 │                                       │
 │── REGISTER (inicial, sin protección) >│
 │   From/To: sip:<IMPU>@<domain>        │
 │   Via/Contact: <IMS-IP>:<port-s>      │
 │   Security-Client: ipsec-3gpp;        │
 │     alg=hmac-md5-96;ealg=null;        │
 │     prot=esp;mod=trans;               │
 │     spi-c=<SPI_c>;spi-s=<SPI_s>;     │
 │     port-c=<port_c>;port-s=<port_s>  │
 │   Require: sec-agree                  │
 │   Proxy-Require: sec-agree            │
 │   P-Access-Network-Info: <ANI>        │
 │<── 401 Unauthorized ──────────────────│
 │    WWW-Authenticate: Digest           │
 │      realm=<IMS_domain>               │
 │      nonce=<RAND‖AUTN hex>            │
 │      algorithm=AKAv1-MD5             │
 │    Security-Server: ipsec-3gpp;       │
 │      alg=hmac-md5-96;ealg=null;       │
 │      spi-c=<P_SPIc>;spi-s=<P_SPIs>; │
 │      port-c=<P_portc>;port-s=<P_ports>│
 │                                       │
 │  [AKA en la SIM: RAND+AUTN → RES, IK, CK]
 │  [instalar 4 SA IPsec en XFRM del kernel]
 │                                       │
 │== REGISTER (protegido, ESP) =========>│
 │   Via/Contact: <IMS-IP>:<port-s>      │
 │   Authorization: Digest               │
 │     algorithm=AKAv1-MD5              │
 │     response=<MD5(RES como contraseña)>
 │   Security-Verify: ipsec-3gpp;...    │
 │<== 200 OK ============================│
 │    P-Associated-URI: sip:+<MSISDN>@..│
 │    Service-Route: ...                 │
 │    Expires: 3600                      │
 │                                       │
 │== SUBSCRIBE (reg-event) =============>│
 │<== 200 OK + NOTIFY ====================│
```

#### Encabezados SIP del REGISTER

- **From / To**: `sip:<IMPU>@<domain>`; sin ISIM, IMPU = `sip:<IMSI>@ims.mncXXX.mccYYY.3gppnetwork.org`.
- **Via / Contact**: nuestro puerto de servidor protegido. `Contact: <sip:<user>@<IMS-IP>:<port-s>;transport=tcp>;expires=…;+sip.instance="<urn:gsma:imei:...>";+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel";+g.3gpp.smsip`
- **Conexiones**: el primer REGISTER va por TCP plano desde un puerto efímero hacia P-CSCF:5060;
  tras el 401 el daemon escucha en `port-s`, instala las SA y abre una conexión protegida
  desde `port-c` → el `port-s` del P-CSCF. El REGISTER protegido lleva `Security-Verify` (un eco de `Security-Server`).
- **Security-Client**: ofrecemos `hmac-md5-96` / `null` (TS 33.203 §7). Cada REGISTER sobre
  las SA vigentes lleva SPI nuevos y un port-c nuevo, con el mismo port-s (ver "Refresco y re-REGISTER" más abajo).
- **P-Access-Network-Info** (PANI): `3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=<PLMN><TAC><ECI>`.
  Obligatorio: el registro no avanza cuando no se puede construir la información de la red de acceso (§75).
- **Require / Proxy-Require**: `sec-agree` (RFC 3329).
- **Supported**: `path, gruu, sec-agree`.
- **P-Preferred-Identity**: tras el registro = el primer P-Associated-URI (no el IMPU temporal
  derivado del IMSI); de lo contrario las llamadas salientes reciben `403 Invalid User` (§88).

#### SA IPsec (XFRM / TS 33.203)

4 SA unidireccionales:

| SA | Origen | Destino | SPI del receptor |
|----|---------|------------|----------------|
| UE-uc → P-CSCF-ps | UE:port-uc | P-CSCF:port-s | P-CSCF SPI-s |
| UE-us → P-CSCF-pc | UE:port-us | P-CSCF:port-c | P-CSCF SPI-c |
| P-CSCF-pc → UE-us | P-CSCF:port-c | UE:port-s | UE SPI-s |
| P-CSCF-ps → UE-uc | P-CSCF:port-s | UE:port-c | UE SPI-c |

- `port-c` y `port-s` son nuestros puertos protegidos de cliente y servidor (aleatorios, del rango
  20000–59999), anunciados en Security-Client. En la tabla también se llaman `port-uc` y `port-us`.
- Algoritmos: `hmac-md5-96` para integridad (clave IK, 16 bytes), cifrado `null`.
- Claves: IK de la respuesta AKA para la integridad de las SA; CK se ignora (ealg=null).
- Modo: transporte (no túnel).
- Cada plantilla de estado y de política tiene `reqid 0x4d554449` (la palabra ASCII
  `MUDI`). Es una etiqueta de propiedad compartida por todas las SA de MudiVoLTE; el SPI sigue
  seleccionando una SA en particular.

Se instalan a través de la interfaz netlink XFRM del kernel (`volte/ipsec/netlink_linux.go`, con
`github.com/vishvananda/netlink`), de modo que las claves nunca pasan por una línea de comandos; las mismas
operaciones expresadas como `ip xfrm state add` / `ip xfrm policy add` (`volte/ipsec/xfrm.go`) sirven
como alternativa (`ipsec_tool: "ip"`) y en las pruebas. La eliminación normal es selectiva: las políticas por
selector y los estados por SPI. Antes de su primera sesión y al apagarse, el servicio lista la SAD
y la SPD y elimina solo las entradas que llevan el reqid `MUDI`, incluidas las que dejó un proceso
terminado antes de que corriera su desmontaje en memoria, con la herramienta de `ipsec_tool` (`RemoveOwnedWith` en
`volte/ipsec/owned.go`): por netlink, o con `ip -o xfrm policy list`, un borrado por selector
para cada política nuestra y `ip xfrm state deleteall proto esp reqid 1297433673 mode transport`.
No se usan `flush` ni un `deleteall` sin ese filtro: borrarían otras SA, por
ejemplo una VPN en el mismo router. Un barrido que falla se registra en el log y se reintenta antes de cada
sesión hasta que funciona; no mantiene la línea caída, porque las entradas que quedan no pueden impedir un
registro. Uno nuevo toma puertos y SPI aleatorios nuevos, y si aun así uno de sus selectores
coincidiera con una política obsoleta, el kernel rechaza la adición (`EEXIST`) y el rollback de ese
intento borra por selector, y con ello la política obsoleta, de modo que el siguiente intento entra.

**LooseInboundSrcPort (§82):** el P-CSCF 192.0.2.20 se conecta a nuestro port-s desde
un puerto de origen impredecible (no desde el port-c anunciado en Security-Server). La política de
desencapsulación no debe verificar el puerto de origen de los paquetes entrantes. Opción `LooseInboundSrcPort bool`
en `volte/ipsec/sa.go`.

#### AKA — autenticación con la SIM

El nonce del 401 = RAND‖AUTN (16 bytes cada uno, 32 bytes en total, en hex en `nonce=`).

```
RAND, AUTN = nonce[:16], nonce[16:]
```

Dos formas de invocarla (el daemon usa la segunda):
1. **`AT+QIMSCFG="authenticate",11,...`** — solo en modo External VoLTE; respuesta
   `DB,RES,IK,CK` (§72).
2. **`AT+CSIM` + APDU AUTHENTICATE** (`00 88 00 81 ...`) — siempre funciona; se usa
   en `volte/` y `src/imsd/csim.go` (wwan-go `sim.Card`, §88).

Ante una falla de sincronización: respuesta `DC,AUTS` (CSIM: SW `98 62`); se repite el REGISTER con `auts=<AUTS>`
en Authorization (TS 33.203 §6.1.3, RFC 3310 §3.4), una vez. Código: `volte/reg/reg.go`,
`register()`, la rama `akaRes.SyncFailure()`. Si la SIM rechazó el AUTN por el MAC (la red no conoce
la clave del suscriptor), no hay reintentos: es una entrada de log de nivel error "la red no es auténtica".

#### de-REGISTER y el error 500 conocido

`volte/reg` se desregistra con un REGISTER protegido con `expires=0` y el mismo Contact que en el
registro, y luego elimina las SA. Antes de 0.11.1 el SBC del operador respondía a esto con `500 Server Internal Error` y
`Warning: 399 … "AKA IP+Port conflict"` (la misma respuesta se había observado antes
con `Contact: *`; §75, §76, §79).

**Causa (§94):** el `Security-Client` del de-REGISTER repetía los SPI y puertos de las SA vigentes, mientras que
TS 24.229 §5.1.1.6.2 d exige en él valores nuevos para dos nuevos pares de SA. Según TS 33.203 §7.1
(regla 3), el P-CSCF aborta todo REGISTER cuyo puerto de cliente protegido ya esté en su
tabla de SA. Desde 0.11.1 el de-REGISTER, igual que el refresco (más abajo), ofrece SPI nuevos y un port-c nuevo,
responde a un desafío 401 con una reautenticación completa, y solo entonces elimina las SA. Aún no
verificado en vivo. Si el 500 persiste, sigue sin causar daño: el registro en la red expira por sí solo (≤ 1 h),
las SA se eliminan limpiamente, y el log recibe una línea de nivel WARN al apagarse.

**Sin de-REGISTER sobre un PDN perdido (§134).** Una sesión del servicio que termina porque su PDN
desapareció (`pdn-lost`, `helper-failed`) elimina las SA y los sockets y no envía nada: la dirección
a la que estaba ligado el registro ya no existe, así que la solicitud no llega a nadie, y los 5 s de
espera por su respuesta se sumarían a cada recuperación de la línea. TS 24.229 (V19.7.0) lo permite:
el desregistro iniciado por el usuario es opcional y usa las
SA del contacto que elimina (§5.1.1.6.1); tras la pérdida del bearer para señalización SIP el UE
lo vuelve a levantar y realiza un nuevo registro inicial (anexo L.2.2.1B);
con ese registro el S-CSCF descarta el anterior, cuya dirección de contacto es otra
(§5.4.1.2.1 ítem 2, §5.4.1.2.2 ítem 4A, §5.4.2.1.2 NOTE 2), y un registro que nadie reemplaza
expira (≤ 1 h). El servicio sí se desregistra cuando se detiene y cuando una sesión termina con el
PDN vivo (`rat-fallback`, `registration-failed`). Código: `reg.ErrAccessLost`, la causa que
cancela el contexto de `Run` (`volte/reg/run.go`), y `holdRegistration` en
`src/imsd/service.go`.

#### Refresco y re-REGISTER

- Por defecto `expires=600000 s` en el REGISTER (TS 24.229 §5.1.1.2.1); la red concede 3600 s;
  el refresco ocurre 600 s antes de la expiración (§5.1.1.4.1), es decir, a los 50 minutos.
- **Antes de 0.11.1 un refresco en la red probada nunca tuvo éxito (§93, §94):** el REGISTER repetido
  iba sobre las SA vigentes con el mismo `Security-Client`, el SBC respondía
  `500 Server Internal Error` con `Warning: 399 … "AKA IP+Port conflict"`, y cada
  50 minutos el daemon se registraba desde cero: las SA, las conexiones protegidas y el puerto de escucha
  se desmontaban, y los puertos y SPI eran nuevos. Una llamada en curso en ese momento perdía su Contact.
- **Reglas (TS 33.203 V19.1.0, TS 24.229 V19.7.0; análisis en §94):**
  - Cada REGISTER sobre las SA vigentes que no responde a un desafío (refresco,
    de-REGISTER) lleva spi-c, spi-s y port-c nuevos en `Security-Client`, con el mismo port-s
    (TS 24.229 §5.1.1.4.2 d, §5.1.1.5.3, §5.1.1.6.2 d; TS 33.203 §7.4.0). `Security-Verify` es
    el `Security-Server` de la última autenticación exitosa. Los SPI difieren de los SPI de todas las
    SA existentes, entrantes y salientes (TS 33.203 §7.1).
  - El P-CSCF aborta un REGISTER si el par (IP del UE, port-c de `Security-Client`) ya está en su
    tabla de SA (TS 33.203 §7.1, regla 3). En la red probada esto es exactamente el `500 … "AKA IP+Port conflict"`.
  - El puerto de servidor protegido del UE (port-s, que también es el Contact) no cambia hasta que todos los IMPU
    estén desregistrados (TS 33.203 §7.1, nota 10).
  - Respuestas de la red: 200 — registro refrescado sin autenticación, las SA siguen iguales; 401 —
    reautenticación; 408, 500, 504 y 403 — registro inicial
    (TS 24.229 §5.1.1.4.1); 423 — reintento con `Min-Expires`.
  - Reautenticación (TS 33.203 §7.4.1a, TS 24.229 §5.1.1.5.1): el 401 llega por las
    SA antiguas; las SA nuevas se construyen a partir del `Security-Server` del 401 y nuestra propia oferta; la respuesta
    al desafío va por las SA nuevas desde el port-c nuevo, con el mismo `Security-Client` que en la solicitud
    que recibió el desafío. Hasta el 200 todo lo demás va por las SA antiguas, después del 200 por las nuevas, salvo las transacciones
    ya en curso. Las SA antiguas se borran cuando el siguiente mensaje ha llegado por las nuevas y
    las transacciones han terminado, y en todo caso cuando expiran; las SA nuevas reciben la mayor entre la vida útil de las SA antiguas
    y la vida útil del registro más 30 s. Ante un 403 o un timeout de respuesta las SA nuevas se borran y
    el trabajo continúa sobre las antiguas. Tras el 200 el P-CSCF envía por las SA antiguas hasta que recibe
    un mensaje por las nuevas (TS 33.203 §7.4.2a).
  - Si el primer REGISTER del procedimiento se envió sin protección, el P-CSCF borra las
    SA antiguas por sí mismo tras el 200 (§7.4.2a).
- **Cómo lo hace `volte/reg` (desde 0.11.1):**
  - `Refresh()` envía un REGISTER protegido sobre las SA vigentes con una oferta nueva. Ante un 200
    las SA siguen iguales; el log dice `IMS registration refreshed`. Ante un 401 — reautenticación,
    SA nuevas junto a las antiguas (`IMS registration refreshed with new security associations`).
    Las SA antiguas viven hasta el fin de su vida útil (la vida útil de su registro más 30 s; en la red probada, unos
    10,5 minutos tras el refresco): el daemon no puede ver por qué SA llegó una solicitud al puerto de servidor,
    así que no las borra con el primer mensaje por las nuevas. La conexión desde el port-c antiguo sigue
    abierta el mismo tiempo: las transacciones ya en curso reciben sus respuestas por ella. La suscripción reg-event
    pasa tal cual a las SA nuevas.
  - XFRM: el conjunto antiguo y el nuevo comparten los selectores del puerto de servidor (el port-c del P-CSCF probado
    es siempre 9950; las políticas entrantes no tienen puerto de origen). El kernel no puede mantener dos políticas con el mismo
    selector. Durante la transición la política entrante compartida acepta
    cualquier SA ESP de MudiVoLTE entre las dos direcciones (una plantilla sin SPI pero con el reqid
    de propietario); la política saliente compartida se mantiene
    en la SA antigua y se cambia a la nueva tras el 200 (una actualización de política). Cuando
    se elimina un conjunto, las políticas compartidas se ligan a las SA del conjunto restante, que queda exactamente
    como `Install` lo habría configurado por sí solo (`ipsec.Set`: `InstallBeside`, `Supersede`,
    `RemoveBeside`; verificado en el kernel, `TestKernelBeside`).
  - Alternativa: registro inicial con el mismo port-s. El puerto de escucha y las conexiones de la red
    hacia él se mantienen; las SA vigentes siguen funcionando hasta el 200 para las nuevas y se eliminan después. Solo
    si esto también falla, se desmonta todo y el daemon se registra desde cero con puertos nuevos. `Register()`
    hace lo mismo tras perder la conexión protegida o ante un NOTIFY de la red.
  - Los procedimientos de registro (`Register`, `Refresh`, `Deregister`, eliminación de las SA antiguas) se ejecutan
    de a uno: el UE participa en un solo procedimiento a la vez (§7.4.1a).
- **Una llamada a través del re-registro.** El Contact de la llamada (port-s) no cambia, y las conexiones de la red
  hacia él no se cierran. Nuestras solicitudes dentro del diálogo toman `reg.Conn()` en el momento del envío;
  tras el 200 esta es la conexión sobre las SA nuevas. **Limitación:** el registro desde cero
  (la última alternativa) cambia port-s y cierra el puerto de escucha: una llamada en curso
  deja de recibir solicitudes de la red (el BYE del otro extremo, un refresco de sesión). Una llamada
  a través del re-registro no se ha verificado en vivo.
- **Qué buscar en el log del daemon:** una vez cada 50 minutos `IMS registration refreshed` o
  `IMS registration refreshed with new security associations`. `re-REGISTER failed, registering
  afresh` con el texto del Warning: la red rechazó el refresco; luego `registration at the same
  server port failed, starting from scratch`: el SBC tampoco aceptó el port-s antiguo. Análisis de capturas:
  `volte/tools/secagree.py` (puertos, SPI ESP y los encabezados Security-* de cada mensaje SIP).
- `423 Interval Too Brief`: reintento con el `Min-Expires` de la respuesta, en cualquier paso (desde 0.11.1).
- **La expiración concedida.** El binding dura el `expires` de nuestro Contact en el 200, si no su
  encabezado `Expires`, si no lo que pedimos (RFC 3261 §10.2.4), leído sin desbordamiento. Más de lo que
  pedimos cuenta como lo pedido: un registrar puede acortar el intervalo, no alargarlo (§10.3, paso 7).
  Una concesión menor a un minuto se refresca como una de un minuto, a los 30 s, con la advertencia `registration
  granted for less than a minute`. Una expiración de 0 o menos significa que el registrar descartó el
  binding (`the registrar granted the binding no time`): el registro cuenta como fallido, y el
  siguiente llega tras la pausa habitual de 5 s que crece hasta 5 minutos, no de inmediato, ya sea que el 200
  respondiera al primer REGISTER o a un refresco. Antes, un 200 así armaba el refresco de inmediato, y cada
  respuesta lo volvía a armar: REGISTER tras REGISTER a la velocidad del viaje de ida y vuelta (miles por segundo
  en loopback), el tipo de carga por la que un operador puede bloquear una SIM.
- SUBSCRIBE reg-event: tras el 200 OK se envía un `SUBSCRIBE` con `Event: reg` para recibir notificaciones
  sobre el estado del registro (§88, `volte/reg/regevent.go`). El daemon procesa los NOTIFY.
  La red probada concede la suscripción por 600000 s. Desde 0.11.0 se refresca en su propio diálogo 600 s antes
  de la expiración (TS 24.229 §5.1.1.3); una suscripción que la red ha olvidado se reemplaza por una nueva, y el fin
  de la reemplazada no conduce a un re-registro (§92). La expiración de `Subscription-State: active;expires=N`
  en un NOTIFY tiene precedencia sobre la expiración del 200. `terminated` con `reason=timeout` o `deactivated`: una nueva
  suscripción sin re-registro; con cualquier otro motivo y sin contacto terminado en el cuerpo:
  re-registro, como antes. Un SUBSCRIBE fallido se reintenta tras una pausa de 1 a 30 minutos.
- **Reautenticación iniciada por la red (`shortened`, TS 24.229 §5.1.1.5A).** El S-CSCF la solicita
  con un NOTIFY de reg-event en el que nuestro contacto sigue `active`, en un registro activo, con
  `event="shortened"` y en `expires` los segundos restantes (RFC 3680 §5.4 exige entonces el atributo).
  El daemon toma esa expiración y refresca como para un binding concedido ahora por ese tiempo (§5.1.1.4.1:
  a la mitad del tiempo hasta 1200 s, si no 600 s antes del fin); el S-CSCF desafía ese REGISTER
  (§5.4.1.6), así que pasa por el 401, la SIM y SA nuevas como cualquier reautenticación. Log:
  `network asks for re-authentication: registration shortened`. Antes, el evento se ignoraba: el
  refresco quedaba donde lo había puesto el 200 (50 minutos después), y el registro se agotaba con la
  expiración acortada.
  - Un acortamiento solo adelanta el refresco. Un NOTIFY de estado completo repite el último evento de un
    contacto que no ha cambiado desde entonces (§5.4.2.1.2), con menos tiempo restante, y no mueve nada.
  - Un 200 al refresco sin desafío vuelve a temporizar el binding y anula el acortamiento,
    salvo que el acortamiento haya llegado mientras ese REGISTER iba en camino: entonces puede ser más reciente que el
    200 y se mantiene.
  - Si la red volviera a acortar el binding justo después de cada refresco, los refrescos que
    provocan los acortamientos se mantienen separados por un minuto.
  - Un contacto `shortened` sin un `expires` legible se ignora con una advertencia. Otros eventos de un
    contacto activo no cambian nada; un contacto terminado (`expired`, `deactivated`, `probation`,
    `unregistered`, `rejected`) significa re-registro, como antes.
  - No visto hasta ahora en la red probada: la reautenticación allí llegó como un 401 a un refresco.


### 4.2 Llamadas entrantes y salientes

Verificado en vivo en la misma red (§79, §81, §85, §86, §87, §88).

#### Perfil del INVITE desde el P-CSCF (en vivo, §88)

A partir de capturas internas de llamadas en vivo:

- El INVITE llega desde P-CSCF:9950 a nuestro `port-us` (= port-s tal como lo anunciamos).
- `Supported: timer,100rel` (no `Require`).
- Sin precondiciones (`preconditions`).
- `Record-Route`: P-CSCF:port-s (9900).
- `Session-Expires: 1800` sin parámetro `refresher`, y `Min-SE: 600` (captura
  interna): la red pide un temporizador de sesión y nos deja la elección del lado
  que refresca (ver "Temporizador de sesión" más abajo).
- `Reason: Q.850;cause=16` en el BYE en una terminación normal.
- Oferta (SDP): AMR-WB PT=101 (`mode-set=0,1,2`) primero, DTMF telephone-event/16000 PT=116.
  AMR-NB PT=102 (`mode-set=0,2,4,7`) aparece en otras llamadas (§86).

#### Llamada entrante (UAS)

```
P-CSCF           mudi-imsd              Linphone (LAN)
   │                  │                      │
   │── INVITE ───────>│                      │
   │<── 100 Trying ───│                      │
   │                  │── INVITE (fork) ────>│  (todos los bindings)
   │<── 180 Ringing ──│                      │
   │     (100rel si hay Require)             │
   │                  │<── 200 OK ───────────│  (el primero en contestar)
   │                  │── CANCEL ───────────>│  (a los demás)
   │<── 200 OK ───────│                      │
   │── ACK ──────────>│                      │
   │                  │  [puente RTP]        │
   │<== RTP <==========│===================>  │
   │                  │                      │
   │── BYE ──────────>│── BYE ─────────────>│
   │<── 200 OK ────────│<── 200 OK ──────────│
```

**100rel/PRACK:** si el INVITE entrante tiene `Require: 100rel`, el 180 se envía de forma confiable, con `RSeq`
y `Require: 100rel`, y se retransmite (T1, duplicándose hasta T2) hasta el PRACK o hasta 64·T1.
El `200 OK` se retransmite hasta el ACK; sin ACK dentro de 64·T1 la llamada se termina con BYE (§88).

**Rechazar una llamada entrante:** siempre `6xx Decline/Busy/Not Acceptable`; esto le prohíbe
al proxy probar otras ramas (RFC 3261 §16.7). Ante `4xx/5xx` el SBC prueba el contacto CS
(`sip:<number>@192.0.2.42`), el MSC hace CSFB a 3G, y esto tumba el PDN IMS (§87).
Códigos en `src/imsd/callmgr.go`:
- Sin teléfonos / nadie contestó → `603 Decline`
- Línea ocupada → `600 Busy Everywhere`
- SDP inválido → `606 Not Acceptable`
- Número fuera de la lista de permitidos → `603 Decline`

#### Llamada saliente (UAC)

Ruta por el PDN IMS con una oferta AMR-WB + AMR + G.711 + DTMF (AMR solo si está enlazado, ver 4.3). Encabezados:
- `Route: <sip:P-CSCF:port-s;lr>` + Service-Route del REGISTER.
- `P-Preferred-Service: urn:urn-7:3gpp-service.ims.icsi.mmtel`
- `P-Preferred-Identity: <first P-Associated-URI>` (§88: la corrección del 403).
- `Accept-Contact: *;+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`
- `Supported: 100rel`

PRACK para 18x confiables (§88). ACK para 2xx (§88). CANCEL al colgar antes del 200 (§84).

Ante `488 Not Acceptable Here` desde el gateway: el gateway exige AMR/AMR-WB (§88: una oferta solo con G.711
es rechazada por el MSX con `sipsl_CompareCodecListForCommon failed`). Esto requiere un binario
compilado en forma cruzada con AMR-WB y AMR (cgo, opencore-amr + vo-amrwbenc).

#### re-INVITE y UPDATE

Respondemos con un SDP espejado y mantenemos la versión del SDP (§84, `onModify`). Ambas solicitudes también refrescan
el temporizador de sesión.

#### Temporizador de sesión (RFC 4028)

Desde 0.11.0, `volte/call/session.go` (§92). Uno de los lados debe refrescar el intervalo de `Session-Expires`
antes de que termine; de lo contrario el otro lado cuelga.

| Caso | Qué hacemos |
|--------|-----------|
| INVITE entrante con `Session-Expires` sin `refresher`, la red anunció `Supported: timer` (como en la red probada) | 200 con `Session-Expires: N;refresher=uac` y `Require: timer`: la red refresca |
| El INVITE entrante tiene `refresher=uas`, o quien llama no tiene `Supported: timer` | Refrescamos nosotros: UPDATE sin SDP a la mitad del intervalo |
| INVITE entrante sin `Session-Expires` | Sin temporizador |
| Intervalo bajo 90 s, en un INVITE o UPDATE | `422 Session Interval Too Small` con `Min-SE: 90` |
| Nuestro INVITE | `Supported: 100rel, timer` y `Session-Expires: 1800` sin `refresher`; el rol se toma del 2xx (`uac`: nosotros, `uas`: la red); un 2xx sin `Session-Expires` significa sin temporizador |
| `422` a nuestro INVITE | Reintento con `Min-SE` y `Session-Expires` de la respuesta, con el CSeq siguiente, a lo sumo dos veces (TS 24.229 §5.1.2A.1.1) |
| La red debería refrescar pero guarda silencio | A los tres cuartos del intervalo primero refrescamos nosotros (UPDATE, `refresher=uac`); BYE según RFC 4028 §10 solo si eso también falla |
| Respuesta a nuestro refresco | 2xx: el intervalo, el rol y la dirección del par según la respuesta; sin `Session-Expires` se desactiva el temporizador; 422: reintento con `Min-SE`; 491: reintento tras 2,1–4 s (nuestra llamada) o dentro de 2 s (llamada entrante); 481, 408 o 32 s de silencio: BYE de inmediato; otros rechazos y una conexión caída: hasta tres reintentos, luego BYE 32 s antes del fin del intervalo (en intervalos cortos, un tercio del intervalo antes del fin) |
| Nuestro refresco | `Session-Expires` no menor que el mayor `Min-SE` de esta llamada (de respuestas 422 y de las solicitudes de la red), y el propio `Min-SE` se envía junto con él (RFC 4028 §7.4) |
| re-INVITE o UPDATE de la red durante una llamada | Confirmamos en el 200 el `Session-Expires` de la solicitud y reiniciamos el intervalo; sin él se desactiva el temporizador. El 200 a un UPDATE sin SDP va sin SDP; el 200 a un re-INVITE sin SDP lleva nuestra oferta |
| UPDATE antes de que se conteste la llamada | No toca el temporizador: el temporizador pertenece a la transacción INVITE |

En la red en vivo solo se ha verificado la negociación (2026-11-06, §93): a `Session-Expires: 1800` sin
`refresher` en un INVITE entrante respondimos `Session-Expires: 1800;refresher=uac` y
`Require: timer`, la red lo aceptó, y la llamada se cursó. El refresco en sí no se ha verificado: eso requiere una llamada
de más de 31 minutos, de preferencia con una captura. Antes de 0.11.0 no anunciábamos `timer`; cómo se comportaba
la red en llamadas largas en ese entonces no se observó. Qué buscar: cómo refresca la red la sesión
(UPDATE o re-INVITE), y si su re-INVITE justo después de contestar contiene `Session-Expires`
(§85, §87): sin él el temporizador se desactiva, como ocurría antes de 0.11.0. En la llamada de prueba del 2026-11-06
(12 s) la red no envió un re-INVITE.

#### BYE atascado (§85, §87)

Si la conexión TCP hacia el P-CSCF se cerró (RST), el BYE no llega a la red. El daemon espera la respuesta
al BYE a lo sumo 12 s, y luego libera la línea (de lo contrario el siguiente INVITE recibe `486 Busy`).
Código: `callmgr.go`, `Hangup` con un timeout.

#### Dos contactos en el S-CSCF (§87)

El suscriptor tiene dos contactos registrados en paralelo en el S-CSCF:
- Nuestro contacto de host: `sip:<IMPU>@<IMS-IP>:<port>`
- El contacto CS del MSC: `sip:<number>@192.0.2.42` (registro en el dominio CS)

Ante `4xx/5xx` la red prueba el segundo. Ante `6xx` no.

#### El Pixel despierta tarde (§87, sin corrección: comportamiento de Android)

El INVITE llega 3–4 veces; Linphone en Android contesta solo la retransmisión que llega después de su propio
re-registro (un retardo de 3,6–7,6 s desde el primer INVITE). Los primeros INVITE se pierden en el socket.
El daemon envía `100 Trying` de inmediato, y `180 Ringing` solo cuando el teléfono ha contestado.

#### Mensajes malformados desde la red

Un mensaje inválido cuesta a lo sumo su conexión, nunca el daemon (`volte/sipnet`):

- Un `Content-Length` por encima del límite de mensaje (256 KiB) es un error de framing: la conexión se
  cierra con la advertencia `sip stream error, closing`. Antes, un valor cercano al límite de los enteros
  desbordaba la verificación de longitud, y el panic en el bucle de lectura tumbaba todo el daemon.
- Una solicitud sin uno de los campos de encabezado que lleva toda solicitud (`Via`, `From`, `To`,
  `Call-ID`, `CSeq`; RFC 3261 §8.1.1) recibe `400 Missing <name> header field` (§21.4.1) y no sigue
  adelante; sin `Via` no se podría enrutar ninguna respuesta (§18.2.2), así que una solicitud así se descarta, y lo mismo
  un ACK, que no lleva respuesta. Antes, un INVITE sin `Call-ID` hacía caer el daemon en el
  gestor de llamadas.
- Un panic mientras se analiza o procesa un mensaje se registra como error con su stack (`panic while
  handling a SIP message, message dropped`), el mensaje se descarta, y la conexión sigue con
  el siguiente. Un panic en otra parte del bucle de lectura cierra esa conexión (`panic on the SIP read
  loop, closing the connection`): la posición en el stream queda entonces desconocida. Un manejador que tiene
  tomado un lock propio en el momento de un panic puede dejarlo tomado; la protección mantiene la línea arriba, pero el
  error en el log sigue siendo un bug que corregir.


### 4.3 Medios (RTP, AMR-WB, AMR, G.722, G.711, DTMF)

#### RTP (RFC 3550)

- Puertos RTP del daemon: dinámicos (no fijos).
- **SO_BINDTODEVICE a la interfaz IMS** (§88, bug 1): el socket RTP del lado de la red se liga
  explícitamente a `rmnet_dataN`. Sin esto, los paquetes salen por la ruta por defecto
  (PDN de internet `rmnet_data0`): la red no nos escucha; fw4 descarta el RTP entrante desde el SBC
  como un flujo "nuevo".
- Código: `netstack.go`, `SO_BINDTODEVICE` en Go mediante `syscall.SetsockoptString`.

#### AMR-WB (RFC 4867)

- PT=101 o PT=111 (la red probada ha usado ambos; revisar `rtpmap` en el SDP).
- **Modo bandwidth-efficient** (el predeterminado de la red probada, §88). Octet-aligned si `fmtp` lo indica.
- Modo de codificación: el modo más alto permitido por el `mode-set` del par en el SDP (el
  `mode-set=0,1,2` de la red probada → modo 2, 12,65 kbit/s); sin `mode-set`, modo 8 (23,85 kbit/s).
  Cada paquete tiene CMR=15 (sin preferencia).
- Códec: un wrapper cgo en torno a `opencore-amrwb` (decodificación) y `vo-amrwbenc` (codificación),
  enlazado estáticamente. Compilación estática: `volte/media/amrwb`, tag `amrwb_static`.
  Sin cgo (sin AMR-WB ni AMR), solo G.711 (una oferta solo con G.711 es rechazada por algunos gateways del operador).

#### AMR, banda angosta (RFC 4867)

- `AMR/8000`, PT dinámico: la red probada lo ofrece en varios PT con distintos `mode-set`
  (102, 100, 107, 108, 113 en `volte/call/testdata/live_offer.sdp`); respondemos con el primero que
  lista y devolvemos su `fmtp` sin cambios. En nuestras propias ofertas: PT=102,
  `mode-change-capability=2;max-red=0` (el `fmtp` de la propia línea AMR de la red), sin `mode-set`.
- Preferencia: AMR-WB, luego AMR, luego G.711 (`network_codecs` en el log; la clave `codecs` de nivel superior de
  la configuración JSON del daemon la reemplaza, la configuración UCI no la fija). AMR es lo que una interconexión a
  2G/3G o a otra red puede ofrecer sin G.711 (issue #8); GSMA IR.92 lo hace obligatorio para un UE
  VoLTE. Este orden es el de nuestras ofertas. En una respuesta manda el orden del ofertante
  (RFC 3264 §6.1): una oferta que lista AMR antes que G.711 se contesta con AMR. Eso incluye llamadas
  como las de §86 (AMR-NB + PCMU) si allí AMR iba primero (el orden no está registrado aquí): se
  contestaban con PCMU, verificado en vivo, y ahora quedan en AMR, aún sin verificar. Para volver a
  G.711 en ellas: `"codecs": ["AMR-WB", "PCMA", "PCMU"]` de nivel superior (test:
  `TestNegotiateAMRBeforeG711`).
- Bandwidth-efficient por defecto, octet-aligned si `fmtp` lo indica; el formato es el de AMR-WB,
  con los tamaños de trama AMR de la Tabla 1 de RFC 4867: los modos 0–7 (4,75–12,2 kbit/s) llevan 95,
  103, 118, 134, 148, 159, 204 y 244 bits, el SID (FT 8) 39 bits; los FT 9–14 invalidan el paquete
  (se descarta), y AMR no tiene SPEECH_LOST.
- Modo de codificación: el modo más alto del `mode-set` del par (p. ej., `mode-set=0,2,4,7` → modo
  7); sin `mode-set`, modo 7 (12,2 kbit/s). CMR=15 y DTX apagado, igual que en AMR-WB.
- Códec: un wrapper cgo en torno a `opencore-amrnb` (codificación y decodificación, opencore-amr
  0.1.6, `Encoder_Interface_*`/`Decoder_Interface_*` sobre tramas en formato de almacenamiento),
  `volte/media/amrnb`, enlazado estáticamente con el mismo tag `amrwb_static`. Sin cgo, no hay AMR.
- Puente: el tramo IMS remuestrea de 8 kHz a 16 kHz y de vuelta (igual que G.711, más abajo), así que
  los teléfonos reciben AMR en G.722 o G.711. DTMF: telephone-event/8000.
- Aún no probado en una llamada real: queda por verificar en el router con una llamada que negocie
  AMR (por ejemplo, ejecutando el daemon a mano con
  `"codecs": ["AMR", "PCMA", "PCMU"]` en su configuración), y con el operador para las interconexiones.

#### G.722 (ITU-T G.722)

- Se usa entre mudi-imsd y Linphone en la LAN (§85, §87, §90).
- Implementación: `volte/media/g722`, escrita a partir del texto de ITU-T G.722 (11/88); coincide
  bit a bit con ffmpeg 7.1 (§90).
- Las llamadas teléfono↔teléfono lo usan en ambos tramos (§90). El puente no tiene pass-through: decodifica a PCM y vuelve a codificar.

#### G.711 µ-law (PCMU) y A-law (PCMA)

- PT=0 (PCMU), PT=8 (PCMA).
- Visto en llamadas con números telefónicos georgianos (§86: AMR-NB + PCMU), y con una oferta
  solo con G.711.
- Go puro en `volte/media`.
- Remuestreo 8↔16 kHz entre el puente de 16 kHz y todo códec de red de 8 kHz (PCMU, PCMA, AMR): filtro FIR con ventana de Hamming,
  33 coeficientes, historial conservado entre tramas (`src/imsd/lan_codec.go`).

#### DTMF (RFC 4733)

- PT=116 (telephone-event/16000) para sesiones AMR-WB; telephone-event/8000 para AMR y G.711.
- Recepción: un dígito por evento, en el primer paquete de fin.
- Implementación en `volte/media`.

#### Búfer de jitter (puente SIP, §84)

- Prebúfer: 3 tramas (60 ms, `jitter_frames`), techo de 8 tramas (160 ms): las tramas más antiguas se descartan.
- Ocultamiento de pérdidas: hasta 3 tramas se repite la última trama con atenuación (1/2, 1/4, 1/8),
  luego silencio.
- Un ticker de 20 ms para cada lado.


### 4.4 SMS sobre IP

Implementación: `volte/sms/service.go`. Verificado parcialmente (§85, §88; no ha habido SMS en vivo sobre IMS:
el operador entregó los SMS vía CS/SGs en ambas sesiones de prueba, §88).

#### MT SMS (entrante)

```
IP-SM-GW          mudi-imsd
      │                 │
      │── MESSAGE ─────>│  (Content-Type: application/vnd.3gpp.sms)
      │<── 200 OK ───────│
      │                 │  [analizar RP-DATA → SMS-DELIVER → OnMessage()]
      │<── MESSAGE ─────│  (RP-ACK en el cuerpo, In-Reply-To: Call-ID del MESSAGE entrante)
      │── 200 OK ───────>│
```

Ante un error de almacenamiento: `RP-Error Cause=22` (Memory Exceeded); la red conserva el SMS y
reintenta más tarde (§88, la corrección de "los SMS se pierden" de §83).

#### MO SMS (saliente)

```
mudi-imsd          SMSC (PSI desde EF_SMSP de la SIM)
      │                      │
      │── MESSAGE ───────────>│  (RP-DATA con SMS-SUBMIT en el cuerpo)
      │<── 200 OK + RP-ACK ───│  (o RP-Error)
```

- URI del SMSC: `sip:<+E164>@<domain>;user=phone`; la dirección se toma de EF_SMSP de la SIM mediante
  `wwan-go sim.Card` (§88). Si no se puede leer de la SIM, se toma de la configuración.
- `Accept-Contact: *;+g.3gpp.smsip` en el encabezado del MESSAGE.

#### Indicadores de registro para SMS

El Contact del REGISTER lleva `+g.3gpp.smsip` (TS 24.341 §5.3.2.2).
El IP-SM-GW está autorizado a entregar MT SMS a nuestro registro. Por qué el operador no usa
esta vía es una pregunta abierta (§88: puede requerirse una suscripción al IP-SM-GW o un perfil
HLR especial).

Los SMS que llegan vía CS son interceptados por el firmware de GL a través de `sms-forward` (§87).

### 4.5 Puente SIP para teléfonos de la LAN (Wi-Fi del Mudi)

Implementación: `src/imsd/lan_server.go`, `lan_registrar.go`, `lan_sdp.go`, `lan_codec.go`,
`lan_rtp.go`, `ims_leg.go`, `bridge.go`, `callmgr.go` (§84).
Verificado en vivo con Linphone en Android + iOS (§85, §86, §87, §90).

#### Registrador LAN (SIP UAS)

- Dirección: `<LAN-IP>:5060` (UDP + TCP) y `<LAN-IP>:5061` (TLS), por defecto `br-lan` 192.168.8.1. Con
  `option plain '0'` solo queda abierto TLS.
- Conexiones TCP y TLS (`lan_listener.go`): a lo sumo 128 abiertas y 8 desde una misma IP, las últimas 32 de las 128 solo
  para IP desde las que un teléfono ha iniciado sesión; una conexión que supera un límite se resetea apenas se acepta. Una conexión
  se cierra si ninguna solicitud en ella pasa la autenticación digest dentro de 30 s. Una en la que un teléfono ha iniciado sesión no tiene
  timeout por inactividad: Linphone en iOS la mantiene silenciosa en segundo plano y lo despierta un push; el keep-alive TCP de Go
  (15 s de inactividad, 9 sondeos separados por 15 s) la termina una vez que el teléfono ya no está. Un error temporal de `accept` (EMFILE, ENFILE,
  ECONNABORTED) se reintenta tras 5 ms, duplicando hasta 1 s; si el bucle de accept de sipgo termina de todos modos, el error se
  registra en nivel Error y se vuelve a escuchar en el puerto. Los rechazos y cierres se registran a lo sumo una vez por
  minuto cada uno, con un conteo.
- Autenticación digest: MD5, `qop=auth`; 401 al REGISTER, 407 a otras solicitudes.
  Cada desafío recibe un nonce propio: un HMAC ligado a la IP del cliente y válido por una hora, de modo que un
  Authorization interceptado desde otra dirección no funciona. Bajo un mismo nonce `nc` debe crecer y `uri` debe nombrar el destino de
  la solicitud: el mismo encabezado enviado de nuevo recibe un nuevo desafío con `stale=true` y no cuenta como falla.
- Una IP se bloquea por 10 minutos tras 10 fallas seguidas dentro de 5 minutos (§84). Solo cuenta una respuesta a un nonce emitido para esa IP; cualquier otra cosa recibe un nuevo desafío. La suplantación ciega del origen UDP no puede dejar bloqueado a un teléfono. Un atacante que pueda capturar el desafío SIP plano del teléfono sí puede usar su nonce; deshabilite SIP plano para evitar esa exposición.
- re-INVITE, UPDATE y SIP INFO (marcación por tonos) se atienden solo dentro de un diálogo de la llamada actual y solo desde
  el teléfono de ese diálogo (su dirección completa sobre UDP, su host sobre TCP y TLS); cualquier otra cosa recibe `481`. INFO además requiere
  un teléfono autorizado, y las claves SRTP de una reoferta viajan solo dentro de TLS.
- **Nonce ajeno (§90):** tras un reinicio del daemon Linphone llega con un nonce antiguo →
  la respuesta es `401 stale=true`, y no cuenta como falla.
- **`+sip.instance` (RFC 5626, §90):** una cuenta, un binding. Ante un REGISTER
  con el mismo `+sip.instance` pero una dirección distinta, se elimina el binding antiguo.
  Linphone Android (SDK 5.5.37) e iOS (SDK 5.5.39) envían `+sip.instance`.

#### Bifurcación (fork) de la llamada entrante

INVITE desde la red → B2BUA → INVITE a todos los bindings vivos de todos los teléfonos (fork; en una llamada
interna: los bindings del teléfono llamado). El primer `200` toma la llamada, los demás → CANCEL.
Carrera: ACK+BYE a un `200` tardío. A un binding sobre TCP o TLS sin su conexión no se le marca.

Un teléfono que desaparece sin un BYE no retiene la línea para siempre: tras 30 s sin RTP
desde él (una llamada en espera, DTX o un micrófono silenciado pueden detener el medio) el daemon sondea
el teléfono con un OPTIONS dentro del diálogo. Cualquier respuesta excepto `481` mantiene la llamada; el silencio la termina:
BYE al otro tramo, línea liberada.

Si no hay nadie a quien hacer sonar (ningún binding vivo ni teléfono al que enviar push): `603 Decline` de inmediato
(§90: antes era 480 precedido de 183/180, lo que confundía a T-ADS).

#### Push a teléfonos dormidos y fork tardío (§104, §109, §110, §114)

Implementación: `src/imsd/flexiapi.go`, `ringer.go`, `lan_push.go`. Un cierre de sesión explícito
(REGISTER con `Expires: 0` o `Contact: *`) también olvida los parámetros de push guardados del dispositivo;
un REGISTER con parámetros `pn-*` los recupera.

Linphone en iOS detiene su core en segundo plano (`CoreContext.onEnterBackground` → `Core.stop()`):
la conexión se cierra, el binding en el servidor permanece hasta que expira. Solo una notificación push VoIP
(PushKit) del dueño de la app lo despierta, e iOS exige que la app muestre una llamada (CallKit) de
inmediato. Para el Linphone de la App Store el dueño es Belledonne, así que el push pasa por su
FlexiAPI (flexisip-account-manager) en `subscribe.linphone.org`:

- Los parámetros de push (RFC 8599) vienen en la URI del Contact de cada REGISTER: Linphone iOS envía
  `pn-provider=apns`, `pn-param=<TEAM>.org.linphone.phone.voip&remote`,
  `pn-prid=<voip-token>:voip&<remote-token>:remote`. Se guardan por `+sip.instance` en
  `/etc/mudi-volte/push.json` (escrito solo cuando cambia).
- `POST /api/push_notification`, JSON `pn_provider`, `pn_param` (`<TEAM>.org.linphone.phone.voip`),
  `pn_prid` (el token VoIP), `type=call` (FlexiAPI → `flexisip_pusher --apple-push-type PushKit`),
  `call_id`; encabezados `content-type`/`accept: application/json` y `From: sip:<login>@sip.linphone.org`.
  Sin firma, responde `401` con dos desafíos `WWW-Authenticate: Digest` (MD5, SHA-256,
  `qop=auth`, `opaque`); la solicitud firmada (RFC 7616, SHA-256 de preferencia) recibe `200`. Validación de
  FlexiAPI: `pn_param` `^[\w\.]+$`, `pn_prid` `^[\w\-:]+$`, `call_id` `^[\w\-~@.]+$`; un pusher
  fallido → `503`. Una API key de usuario no sirve: está ligada a la IP que la creó.
- La llamada: los bindings vivos reciben el INVITE de inmediato; cada dispositivo sin uno recibe un push con un
  Call-ID del daemon (`mv-` + 24 hex) y cuenta como sonando hasta que termina el tiempo de timbre. Su
  siguiente REGISTER (Linphone tras el push: `Core::pushNotificationReceived` → `healNetworkConnections`,
  una tarea en segundo plano de 20 s) se responde con `200`, y justo después el INVITE con ese Call-ID va
  por la nueva conexión; Linphone lo empareja con la llamada CallKit del push. Sin REGISTER dentro de
  3 s → un segundo push con el mismo Call-ID: si iOS había descargado Linphone, el primer push solo
  arranca la app (`EarlyPushkitDelegate`: una llamada provisoria por ~4 s, sin core, sin REGISTER).
- Un dispositivo que recibió push y se registra cuando el timbre ya terminó (quien llamaba colgó, se contestó en otro lado)
  recibe el INVITE y un CANCEL justo después de su primera respuesta provisional, dentro de 25 s; Linphone
  entonces cierra la llamada como perdida. Cualquier dispositivo que se registre mientras suena una llamada también entra en el fork.
- Medido (Linphone iOS, LAN): FlexiAPI ≈ 1 s; REGISTER 0,7–2,1 s después de que FlexiAPI respondió; una llamada
  desde la red hizo sonar el iPhone bloqueado 1,8 s después de llegar al router, una llamada desde otro
  teléfono 3,1 s; el Linphone despertado mantiene su conexión ≈ 18 s.
- Solo el token del dispositivo y el Call-ID inventado salen del router (hacia Belledonne, luego Apple o Google).
- Linphone Android envía `pn-provider=fcm`, `pn-param=<Firebase project number>`,
  `pn-prid=<FCM registration token>` (el token contiene un `:`); FlexiAPI los toma tal cual y
  su pusher envía un mensaje de datos FCM de alta prioridad (prueba en vivo: `200` en 0,85 s). Linphone Android
  mantiene un servicio en primer plano (`CoreKeepAliveThirdPartyAccountsService`, habilitado por el código QR con
  `keep_service_alive=1`) y su conexión, así que solo recibe push cuando el sistema o un ahorrador de batería
  lo ha detenido. Linphone Android habilita el push por defecto solo para `push_notification_domains`
  (`sip.linphone.org`) y lo quita de otras cuentas una vez, al actualizar desde una versión anterior a
  6.0.4; las cuentas configuradas después mantienen `pn-*` en su Contact.

#### SMS a los teléfonos (§100, §119)

Implementación: `src/imsd/smsbridge.go`, `smslist.go`, `lan_message.go`.

Un SMS del spool de smsd va a cada cuenta de teléfono como un MESSAGE fuera de diálogo desde el número
del remitente. Solo cuentan los archivos de la ranura SIM 1 (`Slot: 1`, o sin línea `Slot`): smsd guarda lo que
recibe una segunda SIM en el mismo directorio, bajo `Slot: 2` (§118).

- Un teléfono Android bloqueado responde tarde: el `200` llegó 16, 12 y 1 s después del MESSAGE (el
  keep-alive de Linphone corría cada 31 s entretanto), pasados los 10 s que espera el daemon (`msgTimeout`).
- Cada intento de un mismo SMS lleva el mismo Call-ID, `sms-<smsKey>@<LAN-IP>` (un fragmento de un texto
  largo sobre UDP: `sms-<smsKey>.<n>@<LAN-IP>`). liblinphone archiva un MESSAGE fuera de diálogo bajo su
  Call-ID (`ChatRoom::getMessageFromSal`), y `ChatMessagePrivate::receive` responde `200` a uno
  que ya tiene sin mostrarlo. En vivo: tres intentos a un Pixel bloqueado, un mensaje en el
  teléfono.
- Un `200` que llega después de la espera ya no tiene transacción. sipgo lo entrega al
  manejador de respuestas no atendidas (`lateResponse`), y aun así cuenta como entregado: no se hace ningún
  intento más hacia ese teléfono.
- Sin ninguna respuesta los intentos se repiten tras 1, 2, 4, 8 y luego cada 15 minutos mientras el
  teléfono siga registrado. Un teléfono ausente recibe el SMS cuando se vuelve a registrar, dentro de
  7 días.

Un mensaje de un teléfono a otro, por login o número corto (`textToPhone`), espera al
destinatario dentro de la propia transacción del remitente:

- La espera es de 30 s (`msgRelayWait`). Un teléfono dormido responde la próxima vez que despierta, y el remitente
  desiste en el Timer F, 64·T1 = 32 s tanto en belle-sip como en sipgo. Una respuesta provisional no
  mueve el Timer F.
- `200` apenas un dispositivo ha respondido `2xx`, hagan lo que hagan los demás. Cuando ningún dispositivo tomó el
  texto y al menos uno podría tenerlo: `202 Accepted` (RFC 3428 §7), que liblinphone muestra como
  entregado, igual que `200` (`SalMessageOp::processResponseEventCb`). `480` solo cuando el texto no puede
  haber llegado: ningún dispositivo conectado, o todos lo rechazaron. Nada se encola para
  mensajes teléfono a teléfono: un teléfono que realmente se fue también recibe `202`, y se pierde el texto.
- `100 Trying` tras 3,5 s sin respuesta (RFC 4320 §4.1: el Timer E del remitente ha alcanzado T2).
- Call-ID `msg-<hash>@<LAN-IP>`, a partir del login del remitente, el destinatario, el Call-ID del remitente y
  el texto, de modo que la misma solicitud oída dos veces sea un solo mensaje para el destinatario. Un texto que Linphone
  vuelve a enviar tiene un Call-ID nuevo y es un mensaje nuevo para el daemon.

#### Puente RTP (red ↔ teléfono)

```
Red (PDN IMS)           mudi-imsd              Teléfono (LAN)
     │                      │                       │
     │  AMR-WB/AMR/PCMU     │  G.722/PCMU/PCMA     │
     │<=================== PCM 16k/8k =============>│
```

- Puertos RTP de la LAN: 40000–40199, solo desde la IP del teléfono en el SDP, RTP simétrico.
- Tramo de red: `SO_BINDTODEVICE` a la interfaz IMS.
- Toda llamada pasa por PCM, también G.722↔G.722 dentro de la LAN (§90): el puente no tiene pass-through.

#### Código QR para configurar Linphone (§90)

El daemon sirve un enlace de aprovisionamiento remoto:
```
http://<LAN-IP>:8060/p/<token>
```
Token: 96 bits aleatorios (24 caracteres hex), nuevo cada vez que se muestra un código (`mudi-imsd phone-url`, que invocan la CLI,
la pantalla del router y las páginas web). Cada enlace es un archivo en `/var/run/mudi-volte/links` (0700), nombrado por
el SHA-256 del token y que contiene el login, una verificación de la contraseña (SHA-256 del login y la contraseña) y la
expiración: 15 minutos, o el tiempo en pantalla de `phone screen` más un minuto cuando este es mayor, a lo sumo un día. Un GET
elimina el archivo, de modo que de dos descargas simultáneas solo una obtiene la configuración, y luego elimina los otros archivos de enlace
del teléfono; HEAD no consume un enlace. Un enlace expirado, un teléfono eliminado o una contraseña cambiada responden 404 y cuentan
como enlace erróneo. A lo sumo 16 enlaces viven a la vez: uno nuevo por sobre ese número termina el que expira primero.
La URL se sirve sobre `http` en la LAN; Linphone la acepta (las reglas ATS/cleartext no se aplican a liblinphone).

Formato XML: `linphonerc` (wiki.linphone.org "Remote Provisioning"). Contiene la cuenta,
`transient_provisioning=1` (se aplica una vez) y los códecs (`audio_codec_N`). Con el cifrado activado,
la cuenta va sobre TLS y `[sip]` recibe `media_encryption=srtp`, `media_encryption_mandatory=1`,
`verify_server_certs=0`, `verify_server_cn=0` (liblinphone: `coreapi/linphonecore.c`,
`certificates_config_read`; solo una ruta a un archivo de CA y una regexp del subject, sin pinning).

#### SRTP hacia los teléfonos

Claves SDES (RFC 4568) en el SDP, por lo tanto solo sobre TLS; SRTP según RFC 3711 (`volte/srtp`, AES-CM con
HMAC-SHA1, claves de 128 y 256 bits, tags de 80 y 32 bits; el Apéndice B de RFC 3711 y los paquetes de referencia de libsrtp
son las pruebas). Linphone con `media_encryption_mandatory=1` acepta solo
ofertas `RTP/SAVP` (liblinphone `offeranswer.cpp`, `areProtoCompatibles`: SAVP con SAVP), así que nuestras
ofertas a teléfonos sobre TLS son `RTP/SAVP`; sus suites por defecto (`srtp_crypto_suites`) son
`AEAD_AES_128_GCM, AES_CM_128_HMAC_SHA1_80, AEAD_AES_256_GCM, AES_256_CM_HMAC_SHA1_80`, y tomamos
la primera AES-CM. Las solicitudes dentro del diálogo a un teléfono sobre TLS van a la dirección de origen de su INVITE
o respuesta (sipgo `DialogUA.RewriteContact`): el teléfono no escucha en ningún puerto.

Código QR en la pantalla del Mudi: `/dev/fb0` RGB565, con `gl_screen` detenido (§90).

#### Limitaciones del puente SIP

- SRTP solo con claves SDES sobre TLS: sin DTLS-SRTP, sin ZRTP, sin SRTCP. Linphone no verifica el
  certificado autofirmado del router, así que TLS protege contra la escucha, no contra un impostor
  activo en el Wi-Fi.
- Sin IPv6.
- El early media de la red no se pasa al teléfono (§84).
- iOS: Linphone se detiene cuando la app pasa a segundo plano (~5 s, §85). Las llamadas entrantes le llegan
  mediante un push (ver arriba), con una cuenta de linphone.org configurada; sin ella, solo mientras la app está abierta.
  Un iPhone sobre UDP no recibe push mientras dure su binding: el servidor no puede distinguirlo de uno vivo.
  Los SMS a un iPhone dormido esperan hasta que Linphone se registre.
- Una segunda llamada → `486 Busy Here` (una línea por SIM, §84).
- Límite UDP de 1300 bytes: la respuesta al REGISTER incluye solo el propio Contact del teléfono, más otros contactos mientras
  la respuesta se mantenga ≤1300 bytes (TCP no tiene límite). RFC 3261 §10.3 dice `SHOULD`. De lo contrario Linphone no
  recibe el 200 OK sobre UDP y retransmite según el Timer E (§90).

---

## 5. Problemas conocidos

| # | Problema | Síntoma | Causa | Corrección / Solución alternativa | § |
|---|---------|---------|---------|--------------|---|
| **G1** | Trampa de SUB2 en el proxy AT de GL | QCFG ims → `0,0` en lugar de `1,1`/`2,0`; la configuración no se escribe | El poller en segundo plano de GL cambia el contexto AT entre ranuras | Cada línea empieza con `AT+QUIMSUB=1;` y pasa por el proxy con `sub_id:1`; para QIMSCFG, un `QUIMSUB=1` aparte + el comando | §69, §117 |
| **G2** | QEFSSYNC antes de CFUN=1,1 | La configuración de `ims` y `voice_disable` se revierte tras un reinicio | El reset duro CFUN=1,1 no guarda la NV sin `QEFSSYNC=0` | Siempre `AT+QEFSSYNC=0` antes de `CFUN=1,1` | §71 |
| **G3** | El primer P-CSCF (192.0.2.20) no cumple la norma | Las solicitudes entrantes no se entregan; NOTIFY/INVITE descartados en silencio | El P-CSCF se conecta a port-s desde un puerto de origen impredecible | `LooseInboundSrcPort=true` en ipsec.Set; poner 192.0.2.28 primero en la configuración | §82, §81 |
| **G4** | conntrack genérico de 200 s | El INVITE/NOTIFY entrante se rechaza con ICMP port-unreachable tras ~200 s de silencio | La interfaz IMS no está en una zona de fw4; la entrada de conntrack para el proto genérico 50 expira | Regla `/usr/share/nftables.d/chain-pre/input/20-mudi-volte.nft` | §87 |
| **G5** | 4xx/5xx → CSFB → caída del PDN IMS | Una llamada entrante rechazada → el operador prueba CS → el módem pasa a 3G → el PDN muere | T-ADS prueba el contacto CS ante un código de rechazo no fatal | Rechazar solo con `6xx` (`603 Decline`, `600 Busy`, `606 Not Acceptable`) | §87 |
| **G6** | REGISTER repetido y de-REGISTER → `500 AKA IP+Port conflict` | El refresco del registro falla, y el daemon se registra desde cero cada 50 minutos; el desregistro falla | `Security-Client` repetía los SPI y el port-c de las SA vigentes; el P-CSCF rechaza un REGISTER con un port-c que ya está en uso (TS 33.203 §7.1, regla 3) | Desde 0.11.1 cada REGISTER sobre las SA vigentes tiene SPI nuevos y un port-c nuevo, con el mismo port-s (4.1, "Refresco y re-REGISTER"); no verificado en vivo | §75, §79, §93, §94 |
| **G7** | El PDN IMS muere mientras el daemon está corriendo | `503 bind: cannot assign requested address`; el daemon cree que está registrado | El PDN se cayó (CGACT 2,0), y el daemon no lo monitoreaba; la dirección IMS desapareció de la interfaz | Monitoreo del PDN en `service.go` (§89): `DSI_EVT_NET_NO_NET` → levantar el PDN | §81, §87, §85 |
| **G8** | El módem pasa a UTRAN tras un rechazo IMS | El PDN IMS muere, las llamadas siguientes van al buzón de voz | T-ADS/VLR prueba CS en CSFB; el módem pasa a 3G, y esto rompe el PDN IMS | `voice_domain=1` + `ue_usage=1` (centrado en datos); ante un paso a 3G: `CFUN 4→1` + volver a levantar el PDN + re-registrar | §87, §90 |
| **G9** | TAC +6 vs +10 en QENG | PANI contiene el EARFCN en lugar del TAC | Tras `"LTE"` en QENG, el TAC es el décimo campo, no el sexto | Analizar a partir del token `"LTE"` y contar 10 campos | §85 |
| **G10** | QENG multilínea en EN-DC | El parser falla o toma los campos equivocados | En LTE+NR5G-NSA el módem responde con 3 líneas `+QENG:` | Reunir los campos de todas las líneas `+QENG:`; analizar por búsqueda de tokens | §85 |
| **G11** | Bindings duplicados de un mismo teléfono | Una llamada se envía por fork a una dirección muerta; `devices=2` para un solo iPhone | La clave del binding es login+host:port; cuando cambia el puerto, el binding antiguo no se elimina hasta que expira | `+sip.instance` (RFC 5626): ante un registro con la misma instancia → eliminar el binding antiguo | §90 |
| **G12** | 200 OK al REGISTER > 1300 bytes sobre UDP | El iPhone retransmite el REGISTER según el Timer E y no recibe respuesta | sipgo se niega a enviar un datagrama UDP ≥ MTU | Poner solo el propio Contact del teléfono en el 200 OK, y otros contactos solo mientras el tamaño se mantenga ≤ 1300 bytes | §90 |
| **G13** | RTP de llamadas salientes vía rmnet_data0 | La red no escucha al UE; fw4 bloquea el RTP entrante desde el SBC | Sin `SO_BINDTODEVICE`, el RTP sale por la ruta por defecto (PDN de internet) | `SO_BINDTODEVICE` a la interfaz IMS para todos los sockets de medios | §88 |
| **G14** | El registrador LAN es solo en memoria | Tras un reinicio del daemon los teléfonos "desaparecían"; una llamada entrante → `480` | Los bindings se guardaban solo en RAM | `phones.json`: almacenamiento persistente de los bindings (§89) | §87, §89 |
| **G15** | REGISTER sobre TCP sin protección (rekey de sec-agree) | El primer REGISTER no fue por ESP; el SBC emitió SPI nuevos (401#2) | El segundo REGISTER (con Authorization) salió desde el puerto sin protección porque no se esperó a las SA | Rekey: el SBC puede enviar un segundo 401 con SPI nuevos; manejarlo correctamente (RFC 3329) | §78 |
| **G16** | El SBC cierra el TCP sin protección tras ~80 s | Un WARN en el log sobre un RST en la conexión auxiliar | Comportamiento normal del SBC | Solo un WARN informativo; no afecta el funcionamiento | §77 |
| **G17** | `firstboot -y` no hace nada en el E5800 | Tras un "reset" el router arranca con toda su configuración, sus paquetes y la contraseña antigua | `jffs2reset` busca una partición `rootfs_data`; aquí el overlay es la partición `systemrw` | Restablecimiento de fábrica solo con los medios propios de GL (Reset Firmware → `AT+QCFG="ResetFactory"`); verificar el resultado por el contenido de `/overlay/rootfs-upper`, no por el hecho de que el router se haya reiniciado | §91 |

---

## 6. Referencias

Normas efectivamente usadas en el proyecto (según se mencionan en el código y en el log):

### IETF RFC

| RFC | Título | Dónde se usa |
|-----|----------|----------------|
| RFC 3261 | SIP: Session Initiation Protocol | Protocolo base; §10.3 sobre el SHOULD para la lista de contactos |
| RFC 3262 | Reliability of Provisional Responses (100rel/PRACK) | `inbound.go`: entrega confiable de 180 Ringing |
| RFC 3310 | HTTP Digest Authentication Using AKA | Autenticación AKA; el algoritmo `AKAv1-MD5` |
| RFC 3311 | UPDATE Method | re-INVITE / UPDATE en `call/inbound.go` |
| RFC 3329 | Security Mechanism Agreement for SIP (sec-agree) | Security-Client / Security-Server; rekey |
| RFC 3680 | SIP Event Package for Registrations (reg-event) | SUBSCRIBE / NOTIFY del registro |
| RFC 4303 | IP Encapsulating Security Payload (ESP) | SA IPsec; §2.1 sobre los SPI reservados < 256 |
| RFC 3550 | RTP: A Transport Protocol for Real-Time Applications | Puente RTP, marker tras un hueco en el flujo |
| RFC 4028 | Session Timers in SIP | `volte/call/session.go`: ambos roles, refresco vía UPDATE; 4.2, temporizador de sesión |
| RFC 4733 | RTP Payload for DTMF Digits and Telephony Tones | telephone-event, PT=116 |
| RFC 4867 | RTP Payload Format and File Storage Format for AMR/AMR-WB | AMR-WB y AMR, bandwidth-efficient y octet-aligned; §88, §63 |
| RFC 5626 | Managing Client-Initiated Connections in SIP (+sip.instance) | Bindings con un UUID de instancia |
| RFC 7254 | A Uniform Resource Name Namespace for the GSMA and the IMEI | `urn:gsma:imei:...` en el Contact |
| RFC 7616 / RFC 2617 | HTTP Digest Access Authentication | Autorización de los teléfonos de la LAN (MD5, `qop=auth`) |

### 3GPP TS

| TS | Título | Dónde se usa |
|----|----------|----------------|
| TS 23.003 §13 | Numbering, addressing and identification — IMPU/IMPI from IMSI | Derivación de las identidades sin ISIM |
| TS 24.229 | IP multimedia call control protocol based on SIP and SDP | Registro §5.1.1 (refresco, reautenticación y de-REGISTER §5.1.1.4–§5.1.1.6 contrastados con V19.7.0, §94); sin de-REGISTER sobre un PDN perdido: §5.1.1.6.1, anexo L.2.2.1B, §5.4.1.2.1, §5.4.1.2.2, §5.4.2.1.2 (§134); PANI §7.2A.4, P-Preferred-Identity §5.1.2A.1.1, `Expires` §5.1.1.2.1 |
| TS 24.341 | Support of SMS over IP | SMS MESSAGE; §5.3.2.2 `+g.3gpp.smsip`; §5.3.2.4 RP-ACK al SMSC; §5.3.1.2 MO |
| TS 24.011 | Point-to-Point SMS support on mobile radio interface | RP-DATA / RP-ACK / RP-ERROR, causas (tabla 8.4) |
| TS 23.040 | Technical realization of SMS | TPDU: SMS-DELIVER / SMS-SUBMIT, GSM-7, UCS-2, concatenación |
| TS 27.007 §10.1.23 | AT+CGCONTRDP | Campos de la respuesta: posiciones de DNS y P-CSCF |
| TS 33.203 | 3G Security — Access security for IP-based services | §7: IPsec ESP en modo transporte, IK/CK, algoritmos; §7.1: elección de SPI y puertos, verificaciones del P-CSCF; §7.4: reautenticación y cambio de SA (contrastado con V19.1.0, §94); §6.1.3: resincronización del SQN |

### Quectel

| Documento | Dónde se usa |
|----------|----------------|
| External VoLTE Stack Application Note V1.0 (RG520N/RM5x0N, 2023-12-21) | Procedimiento QCFG ims,2 + QEFSSYNC + dsi + CGCONTRDP, QIMSCFG; §68, §71 |
| AT Commands Manual RG520N/RG50xQ | QMAP MPDN, QCFG; §65, §68 |
| AT Commands Manual RG65xE/RG650V/RM550V/RM551E V1.0 (Released, 2024-07-11) | Valores de `voice_domain` y `ue_usage_setting`, `QEFSSYNC`, retardo de escritura de la NV; contraste en 2.9 |

### ITU-T

| Norma | Dónde se usa |
|----------|----------------|
| ITU-T G.722 (11/88) | Implementación en `volte/media/g722`; §90 |

---

*Compilado el 2026-11-04 a partir del registro interno de ingeniería (§62–§91) y del código del repositorio; el 2026-11-05 contrastado
con el código de 0.9.14 y corregido; el 2026-11-06 se agregaron el contraste con el manual AT del RG650V (2.9), el temporizador de sesión y el refresco de la suscripción (4.1, 4.2). Las secciones sobre la red en vivo describen a un operador europeo;
con otro operador, los detalles de sec-agree, del P-CSCF y de la entrega de SMS pueden diferir.*
