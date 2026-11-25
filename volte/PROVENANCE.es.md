[English](PROVENANCE.md) | **Español**

> Traducción informativa. En caso de discrepancia prevalece la versión en inglés.

# PROVENANCE: de dónde viene el código de volte

La biblioteca está escrita a partir de las especificaciones abiertas de los protocolos y de
lo que hace la red real.

## Reglas

1. **El comportamiento proviene de las especificaciones y de nuestras propias observaciones:**
   - 3GPP TS 24.229 (IMS SIP), TS 33.203 (seguridad de acceso IMS), TS 24.341 (SMS
     sobre IP), TS 26.114 (medios MTSI), TS 24.011 / 23.040 (PDU de SMS);
   - IETF RFC 3261, 3262, 3311, 3310, 2617, 3329, 3312, 3680, 4867, 4733, 3550;
   - `FINDINGS.md` (el registro de ingeniería interno del proyecto MudiVoLTE, no
     publicado), capturas y logs de la red real (no publicados), las respuestas AT
     del módem.
2. Cada paquete nombra en el encabezado de su `doc.go` las especificaciones a partir de las
   cuales está escrito.

## Dependencias de terceros

| Componente | Licencia | Propósito |
|---|---|---|
| github.com/emiago/sipgo | BSD-2-Clause | Parser SIP, transacciones, transporte |
| github.com/damonto/wwan-go | MIT | Transporte AT/SIM (AT+CSIM) |
| github.com/vishvananda/netlink (con github.com/vishvananda/netns) | Apache-2.0 | Estados y políticas XFRM a través de netlink |
| opencore-amr, vo-amrwbenc | Apache-2.0 | Decodificador y codificador AMR-WB, codificador y decodificador AMR-NB (opencore-amr) |

## Registro

- 2026-11-03: se crea el esqueleto; `auth` (AKA Digest, sec-agree, claves) e
  `ipsec` (4 SA y políticas, representadas como `ip xfrm`) escritos a partir de RFC
  3310/2617/3329 y TS 33.203 §6.1, §7.1, §7.4.
- 2026-11-03: `sipnet` (framing TCP RFC 3261 §18.3, keep-alive RFC 5626,
  correspondencia de transacciones §17.1.3, puertos propios para sec-agree), `sim` (la interfaz
  AKA, TS 31.102/33.102), `reg` (TS 24.229 §5.1.1: REGISTER inicial y protegido,
  resincronización AUTS, refresco §5.1.1.4.1, de-REGISTER, reg-event RFC 3680).
  De sipgo solo se usa el paquete `sip` (parsing).
- 2026-11-03: `call` (TS 24.229 §5.1.3–5.1.4, RFC 3261 §12–§17, RFC 3262, 3264,
  3311, RFC 4566/4867 para SDP; GSMA IR.92). La oferta de ejemplo
  `call/testdata/live_offer.sdp` proviene de nuestra propia captura de la red real (lado de la
  red), con las direcciones reemplazadas por direcciones de documentación (RFC 5737). `tools/pcapsip.py` es nuestro propio parser de pcaps con ESP-null.
- 2026-11-03: `media`: RTP según RFC 3550, payload AMR-WB según RFC 4867 (BE y
  OA, formato de almacenamiento §5.3), tamaños de trama según TS 26.201, G.711 según ITU-T G.711 y
  el algoritmo de G.191, DTMF según RFC 4733, WAV. `media/amrwb` es un binding cgo a
  libopencore-amrwb y libvo-amrwbenc del sistema (Apache-2.0); los prototipos E_IF_* y
  D_IF_* se declaran a mano a partir de sus headers públicos. Las tablas de decodificación
  de G.711 en `media/testdata` se generaron con ffmpeg.
- 2026-11-03: `sms`: TS 24.341 §5.3 (MESSAGE, el reporte al SMSC por
  P-Asserted-Identity con In-Reply-To), TS 24.011 §7.3/§8.2 (RP-DATA, RP-ACK,
  RP-ERROR, causas), TS 23.040 §9.2 (SMS-DELIVER y SMS-SUBMIT, UDH de
  concatenación), TS 23.038 §6.2 (GSM 7-bit con la tabla de extensión, UCS-2). Las
  PDU de referencia en las pruebas son ejemplos conocidos de libros de texto.
- 2026-11-04: `media/g722`: G.722 a 64 kbit/s (SB-ADPCM, modo 1) en Go puro
  en lugar de `gotranspile/g722` (ese código traducido no tiene licencia). Escrito
  a partir del texto de ITU-T G.722 (11/88) de itu.int: el QMF de §5 (Tabla 11),
  los subbloques ADPCM de §6.2 bit-exact (Tablas 13–21). El código de otras
  implementaciones (gotranspile/g722, spandsp, ffmpeg, el código de referencia de la ITU)
  no se abrió; ffmpeg 7.1 sirve solo como caja negra en las pruebas, y los
  vectores en `media/g722/testdata` se generaron con él. Donde ffmpeg difiere
  del texto: en el QMF (la aritmética queda a criterio de la implementación, §5.2
  nota 1) seguimos a ffmpeg, 16 bits de entrada y salida, XL/XH sin el límite de 15 bits
  LOWT/HIGHT; en el ADPCM nos ceñimos a §6.2, mientras que ffmpeg suma FILTEZ
  sin saturación intermedia (visible solo en flujos artificiales, prueba
  `TestFILTEZOrder`).
- 2026-11-06: la licencia de la biblioteca es PolyForm Noncommercial 1.0.0 (decisión
  del autor: abierta, no comercial). El texto en `LICENSE` es literal
  del repositorio polyformproject/polyform-licenses (tag 1.0.0), la
  línea `Required Notice` está en `NOTICE`. Las dependencias bajo BSD-2-Clause,
  MIT y Apache-2.0 son compatibles con ella: conservan sus propias licencias, y
  nada de su código está en este repositorio.
- 2026-11-06: `reg` e `ipsec`: re-registro según el texto de TS 33.203
  V19.1.0 (§7.1, §7.4.0, §7.4.1a, §7.4.2a) y TS 24.229 V19.7.0
  (§5.1.1.4–§5.1.1.6), archivos de 3gpp.org: SPI nuevos y un port-c nuevo en
  cada REGISTER enviado sobre las SA en uso, reautenticación con las SA nuevas
  junto a las antiguas (políticas XFRM con selectores compartidos, a partir de la
  documentación de iproute2 y de pruebas en un network namespace), registro inicial en el
  puerto de servidor antiguo. `tools/secagree.py` es propio, modelado a partir de
  `tools/pcapsip.py`.
- 2026-11-07: `srtp`: SRTP según RFC 3711 (AES-CM §4.1.1, HMAC-SHA1 §4.2, derivación
  de claves §4.3, la estimación del índice del Apéndice A, la ventana de replay de
  §3.3.2), las suites AES-256 de RFC 6188 y el atributo SDES de RFC 4568.
  Las pruebas usan los vectores de RFC 3711 Apéndice B.2 y B.3 y los valores de bytes
  de los paquetes de referencia en `test/srtp_driver.c` de libsrtp
  (BSD-3-Clause); sin código de libsrtp. Fuera de este repositorio el paquete se
  verificó de forma cruzada contra pion/srtp (MIT) paquete por paquete; no es una
  dependencia.
- 2026-11-08: `ipsec` instala las SA y las políticas a través del socket netlink de XFRM
  con `github.com/vishvananda/netlink` (Apache-2.0) en lugar de
  ejecutar `ip xfrm`: las claves ya no pasan por una línea de comandos. La
  representación como comandos `ip xfrm` se mantiene como respaldo y en las pruebas; los
  mensajes netlink se mapean a partir de las mismas operaciones.
- 2026-11-13: `reg`: `Run` termina sin un de-REGISTER cuando su contexto se
  cancela con la causa `ErrAccessLost`, según el texto de TS 24.229 V19.7.0
  (el mismo archivo): §5.1.1.6.1 (el UE puede des-registrarse, sobre las SA del
  contacto), anexo L.2.2.1B (un nuevo registro inicial tras perderse el bearer de
  señalización SIP), §5.4.1.2.1, §5.4.1.2.2 y §5.4.2.1.2 (el nuevo
  registro reemplaza al antiguo en el S-CSCF).
- 2026-11-25: `media`: el payload AMR (banda angosta) según RFC 4867 (BE y OA,
  formato de almacenamiento §5.3; tamaños de trama de su Tabla 1, tomada de
  TS 26.101) comparte el código de empaquetado con AMR-WB. `media/amrnb` es un
  binding cgo a libopencore-amrnb del sistema (Apache-2.0); los prototipos
  Encoder_Interface_* y Decoder_Interface_* se declaran a mano a partir de sus
  headers públicos (`amrnb/interf_enc.h`, `amrnb/interf_dec.h` de
  opencore-amr 0.1.6, el tarball orig de Debian fijado en
  `package/toolchain/fetch-and-build.sh`).
