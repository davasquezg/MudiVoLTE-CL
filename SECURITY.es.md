[English](SECURITY.md) | **Español**

> Traducción informativa. En caso de discrepancia prevalece la versión en inglés.

# Seguridad

MudiVoLTE pone una línea telefónica en un router doméstico: una falla en él puede permitir que alguien en
el Wi-Fi escuche llamadas, lea SMS o haga llamadas a través de la SIM a costa del
dueño. Los reportes de este tipo de fallas son bienvenidos y se tratan primero en
privado.

## Reportar una vulnerabilidad

Usa el reporte privado de vulnerabilidades de GitHub: la pestaña **Security** de
<https://github.com/xarvel/MudiVoLTE>, "Report a vulnerability". Si ese
formulario no está disponible, abre un issue que diga solo que tienes un reporte
de seguridad y cómo contactarte; deja los detalles fuera del issue.

Incluye la versión (`mudi-volte version`), el firmware del router,
lo que necesita un atacante (un lugar en el Wi-Fi, una cuenta de teléfono, la contraseña
de administración del router...) y cómo reproducirlo. Los logs y las capturas contienen
números de teléfono, el IMSI y tokens de push: enmascáralos antes de enviarlos.

MudiVoLTE es un proyecto personal sin equipo de seguridad. Deberías recibir respuesta
dentro de una semana; las correcciones salen como una nueva versión y el reporte se acredita ahí,
a menos que prefieras no ser nombrado.

## Qué está dentro del alcance

- El servidor SIP, los tramos RTP y el aprovisionamiento por código QR en el lado LAN
  (`src/imsd/lan_*.go`, `provision.go`): cualquier cosa que un dispositivo en la
  red del router pueda hacer más allá de lo que permite su cuenta de teléfono, como escuchar
  o secuestrar una llamada, hacer llamadas como otro teléfono, leer SMS o
  registrar un teléfono sin su código QR.
- Los push a través de subscribe.linphone.org (`flexiapi.go`): cualquier cosa que
  filtre más que el token de push y un ID de llamada aleatorio.
- Las páginas web y sus módulos RPC (`package/files/www/`,
  `package/files/usr/lib/oui-httpd/rpc/`), el script de gestión y el
  instalador: cualquier cosa que un visitante del panel de administración o una actualización del paquete pueda
  hacer y que el administrador del router no haya pedido.
- El cliente IMS (`volte/`): cualquier cosa que un P-CSCF hostil o una celda falsa pueda hacerle
  al router más allá de negar el servicio.

## Contra qué no protege el proyecto

La sección "Cifrado" del README describe el modelo de amenazas. En resumen:

- Quien ya tiene la contraseña de administración del router o un acceso SSH es dueño de
  la línea telefónica.
- Linphone no puede fijar el certificado autofirmado del router, por lo que un host en el
  Wi-Fi que se hace pasar activamente por el router no es detectado; el enlace del código QR
  en sí es HTTP plano (funciona una sola vez y durante 15 minutos).
- Los teléfonos configurados a mano sobre UDP hablan y se registran en claro; la opción
  `plain '0'` cierra esos puertos una vez que todos los teléfonos están en TLS.
- Las llamadas a números comunes nunca se cifran de extremo a extremo, y no se garantiza
  que las llamadas de emergencia funcionen.

## Versiones soportadas

Solo la última versión recibe correcciones. La página de versiones indica la versión y
el checksum de cada paquete.
