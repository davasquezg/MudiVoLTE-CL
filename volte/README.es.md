[English](README.md) | **Español**

# volte: un cliente VoLTE del lado del host (IMS UE) para MudiVoLTE

`volte` es la biblioteca bajo `mudi-imsd`, el daemon de MudiVoLTE. Está escrita
a partir de las especificaciones de 3GPP e IETF y cubre un solo escenario: **la SIM está
en un módem Quectel en modo External VoLTE Stack, el host levanta la PDN de IMS,
y todo el cliente IMS corre en Linux (OpenWrt en el GL.iNet Mudi 7)**.

Titular de los derechos de autor: Arthur Valiullin. Licencia: PolyForm Noncommercial 1.0.0
(`LICENSE`, `NOTICE`): se permiten el uso, la modificación y la redistribución con
fines no comerciales; el uso comercial requiere una licencia aparte del
titular de los derechos de autor.

## Qué incluye y qué no

| Incluye | Excluye (fuera del alcance actual) |
|---|---|
| IMS AKA a través de la SIM (AKAv1-MD5, AUTS) | VoWiFi (ePDG, IKEv2, EAP-AKA) |
| sec-agree e IPsec ESP en modo transporte | ESP y TCP/IP en espacio de usuario: ambos son del kernel |
| REGISTER, re-REGISTER, de-REGISTER, reg-event | Entitlement del operador (TS.43) |
| Llamadas entrantes y salientes, PRACK, UPDATE | Adaptadores de PDN QMI/MBIM (el daemon levanta la PDN por sí mismo) |
| RTP, AMR-WB y AMR (RFC 4867), DTMF | EVS, USSD |
| SMS sobre IP (TS 24.341) | Perfiles de otros operadores listos para usar |

## Arquitectura

```
volte/
  auth/    IMS AKA Digest (RFC 3310/2617), sec-agree (RFC 3329), claves de SA (TS 33.203 §6.1, §7)
  ipsec/   el conjunto de 4 SA y políticas según TS 33.203 §7.1, instalado en el kernel (XFRM)
  reg/     registro: inicial -> 401 -> AKA -> SA -> REGISTER protegido -> 200, temporizadores, reg-event
  call/    INVITE UAS/UAC, 100rel/PRACK, UPDATE, BYE/CANCEL, oferta/respuesta SDP
  media/   RTP/RTCP, payload AMR-WB y AMR RFC 4867 (octet-aligned y bandwidth-efficient), telephone-event
  sms/     MESSAGE application/vnd.3gpp.sms: RP/TP (TS 24.011, 23.040)
  sim/     la interfaz de la SIM (AUTHENTICATE); la implementación con AT+CSIM está en imsd
  srtp/    SRTP para el medio de los teléfonos (RFC 3711, AES-CM + HMAC-SHA1) y claves SDES (RFC 4568)
```

Decisiones clave:

- **IPsec se deja al kernel.** El kernel del Mudi tiene `CONFIG_XFRM_USER=y` y
  `CONFIG_INET_ESP=y`, y `ip xfrm` funciona (§62). Las SA y las políticas se instalan
  a través del socket netlink de XFRM (`github.com/vishvananda/netlink`,
  Apache-2.0), de modo que las claves nunca pasan por una línea de comandos; la representación
  como comandos `ip xfrm` se mantiene como respaldo y para las pruebas. Una vez instaladas
  las SA, SIP corre sobre sockets TCP/UDP comunes en los puertos
  protegidos: sin una pila ESP ni TCP propia.
- **Parsing y transacciones SIP: `github.com/emiago/sipgo` (BSD-2-Clause).**
  imsd ya lo usa en el lado LAN.
- **SIM: `github.com/damonto/wwan-go` (MIT).**
- **Códec AMR-WB:** opencore-amr y vo-amrwbenc (Apache-2.0) mediante cgo
  (`media/amrwb`, que se habilita al importarlo). Sin él, G.711 funciona en Go puro;
  la red probada también lo ofrece. Por defecto el códec se enlaza contra las bibliotecas
  `.so.0` del sistema (`link_shared.go`); con el tag `amrwb_static` se enlaza estáticamente
  (`link_static.go`: `-lopencore-amrwb -lvo-amrwbenc -lm`, el directorio con
  los archivos `.a` va en `CGO_LDFLAGS`). La compilación estática con musl para el Mudi es
  `scripts/build-static.sh` del repositorio de MudiVoLTE.
- **Códec AMR (banda angosta):** libopencore-amrnb de opencore-amr (Apache-2.0)
  mediante cgo (`media/amrnb`, que se habilita al importarlo), enlazado de la
  misma forma: `libopencore-amrnb.so.0` por defecto, `-lopencore-amrnb -lm`
  con el mismo tag `amrwb_static`. Sin él, `NewCodec` no tiene AMR y el daemon
  lo deja fuera de su lista de códecs.

## Comportamiento de la red real incorporado desde el inicio

§N cita una sección del registro de ingeniería interno del proyecto, que no está
publicado.

- Las direcciones del P-CSCF vienen en el PCO después de los servidores DNS (§75). La primera
  de la lista (192.0.2.20) envía desde un puerto de origen impredecible, por lo que las
  políticas de entrada en `port-us` **no fijan el puerto de origen** (§81, §82).
- Señalización sobre TCP; sec-agree: `alg=hmac-md5-96; ealg=null; prot=esp; mod=trans`.
- Un re-REGISTER y un de-REGISTER ofrecen SPI nuevos y un port-c nuevo en
  `Security-Client` (TS 33.203 §7.1, §7.4; TS 24.229 §5.1.1.4.2, §5.1.1.6.2):
  ante una oferta del port-c en uso, el SBC responde
  `500 … "AKA IP+Port conflict"` (§93, §94). Tras un desafío 401, las SA nuevas
  se instalan junto a las antiguas; el puerto de servidor del UE (el Contact de las
  llamadas) no cambia mientras dura el registro.
- P-Access-Network-Info lo exige TS 24.229; imsd normalmente lo construye a partir
  de `AT+QENG="servingcell"`. `volte/reg` y `volte/call` lo agregan solo cuando
  tiene valor: si no se ha leído ninguna celda, imsd lo omite con un aviso (ver
  docs/PROTOCOLS.es.md §2.6).

De dónde viene el código: `PROVENANCE.es.md`.
