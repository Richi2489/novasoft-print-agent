# Setup — NovaSoft Print Agent

Guía paso a paso para configurar la impresión térmica en un restaurante
NovaSoft por primera vez.

## Requisitos

- **Windows 10 o 11**, 64-bit.
- **Impresora térmica 80mm** conectada por USB (o Ethernet), con el
  driver ya instalado en Windows y probado con el Bloc de Notas
  (imprime una página en blanco). Modelos validados:
  - **GHIA GTP801** (certified, validada en producción en La Capital).
  - **Cualquier POS-80 USB compatible ESC/POS** (supported).
  - Ver `CERTIFIED_PRINTERS.md` para la lista completa.
- **Conexión a internet** (el agent habla con api.novasoft.mx / Railway).
- **Rol ADMIN o MANAGER** en NovaSoft.

## Instalación recomendada — instalador de doble clic (desde v0.4.1)

1. En NovaSoft: **Configuración → Impresoras → Agregar impresora**, ponle
   nombre y deja a la vista el código (dura 10 minutos).
2. Descarga y abre `NovaSoftAgentSetup.exe`
   (`releases/latest/download/NovaSoftAgentSetup.exe`). Windows pide
   permiso de administrador: acepta. Si SmartScreen avisa, **Más
   información → Ejecutar de todas formas**.
3. Acepta la licencia y **escribe el código**. El instalador lo valida en
   ese momento: si está mal escrito, ya se usó, venció o no hay internet,
   lo dice y te deja corregirlo.
4. **Instalar → Finalizar.** Queda en `C:\Program Files\NovaSoft\PrintAgent`
   como servicio de Windows (arranque automático y reinicio ante fallo).
   NovaSoft detecta la impresora en segundos y ofrece la prueba.

Reinstalar o actualizar: vuelve a correr el instalador y deja el código
vacío — conserva el emparejamiento. Instalación silenciosa para soporte:

```powershell
NovaSoftAgentSetup.exe /VERYSILENT /CODE=ABCD-1234-WXYZ
```

Desinstalar: *Configuración de Windows → Aplicaciones → NovaSoft Print
Agent*. Detiene y quita el servicio; el emparejamiento y los logs se
quedan en `C:\ProgramData\NovaSoftAgent`.

Lo que sigue es la **instalación manual** (sin instalador), útil para
diagnóstico.

## 1 — Descarga el Agent

Descarga `novasoft-agent.exe` desde el último release en:

```
https://github.com/Richi2489/novasoft-print-agent/releases
```

Guárdalo en una ruta fija y estable, por ejemplo:

```
C:\NovaSoftAgent\novasoft-agent.exe
```

**Evita escritorio / carpeta de descargas** — si Windows los limpia,
el agent deja de arrancar.

## 2 — Genera el código en NovaSoft

1. Entra a NovaSoft con tu cuenta (ADMIN o MANAGER).
2. Menú lateral → **Impresión**.
3. **+ Agregar impresora**.
4. Paso 1 → Continuar.
5. Paso 2 → escribe un nombre ("Equipo Caja", "Barra", "Cocina") →
   **Generar código**.
6. El wizard muestra un código como `ABCD-1234-WXYZ`. Tienes 10 minutos
   antes de que expire. Puedes copiarlo al portapapeles con el botón 📋.

Deja esa ventana abierta — al terminar el paso 3, el wizard detectará
el emparejamiento automáticamente.

## 3 — Empareja el Agent

En la computadora de la caja, abre PowerShell **como administrador**
(click DERECHO al menú Inicio → "Terminal (Administrador)" o "Windows
PowerShell (Administrador)") y ve a la carpeta del agent:

> **¿Por qué administrador?** Desde v0.3.0 el emparejamiento se guarda
> en `C:\ProgramData\NovaSoftAgent\`, un directorio de máquina, para que
> el servicio de Windows también lo pueda leer. Escribir ahí requiere
> elevación. Es la única vez que hace falta.

```
cd C:\NovaSoftAgent
```

Ejecuta el comando de emparejamiento:

```
.\novasoft-agent.exe pair
```

El agent pedirá el código:

```
=== NovaSoft Print Agent - Emparejamiento ===
Ingresa el código que aparece en NovaSoft:
```

Pégalo (puedes hacerlo con o sin guiones, en mayúsculas o minúsculas
— el agent lo normaliza) y presiona Enter.

Si todo salió bien:

```
→ Emparejando…

✓ Emparejado correctamente
  Agent ID: 7f3a...
  Config:   C:\ProgramData\NovaSoftAgent\config.json

Ahora instala el servicio para que arranque solo:
  novasoft-agent.exe service install
  novasoft-agent.exe service start
```

En el wizard de NovaSoft verás que avanzó al **Paso 3** automáticamente.

## 4 — Arranca el Agent

Con el emparejamiento hecho, lanza el modo de operación:

```
.\novasoft-agent.exe run
```

> Esto lo deja corriendo **en esta ventana**, que es lo ideal para ver
> que todo funciona la primera vez. Para el uso diario **no dejes el
> agent así**: instálalo como servicio de Windows (sección "Mantener el
> agent corriendo al reiniciar la computadora", más abajo). Es un par de
> comandos y evita que el restaurante se quede sin imprimir si alguien
> cierra la ventana o la máquina se reinicia.

El agent:

1. Lista las impresoras instaladas en Windows.
2. Elige la que parezca térmica (contiene "POS-80", "THERMAL", "GHIA",
   etc.). Si hay varias, usa la primera que coincide.
3. Conecta al servidor de NovaSoft.

Output típico:

```
Impresoras detectadas:
  1. POS-80 - NovaSoft Demo
  2. Brother DCP-T520W
→ Usando: POS-80 - NovaSoft Demo

→ Conectando a wss://api.novasoft.mx/ws/printing
✓ Conectado al servidor NovaSoft
```

**Deja la ventana abierta** — el agent necesita seguir corriendo para
recibir jobs. Cerrarla termina el proceso.

## 5 — Prueba de impresión

En NovaSoft, en el wizard, click **Imprimir prueba**. Debería salir un
ticket con:

```
NOVASOFT
Prueba de impresión
────────────────────────────
Estado:                   OK
Alineación:  izquierda/derecha
────────────────────────────
Si ves este ticket,
la impresora está lista.
```

Si salió: ✓ **configuración completa**. Cierra el wizard.

## 6 — Uso normal

Con el agent corriendo, el botón **"Imprimir cuenta"** del POS envía la
cuenta directa a la impresora — sin abrir PDF, sin pestañas nuevas.

Si el agent está apagado o desconectado, el POS cae al PDF descargable
(comportamiento anterior). El cliente nunca se queda sin opción.

## Comandos disponibles

| Comando | Qué hace |
|---|---|
| `pair` | Empareja usando un código del wizard |
| `run` | Arranca el loop principal en esta consola — escucha jobs |
| `service install` | Instala el servicio de Windows (auto + reinicio ante fallo) |
| `service uninstall` | Detiene y elimina el servicio |
| `service start` | Arranca el servicio |
| `service stop` | Detiene el servicio |
| `service status` | Estado del servicio + ruta del log |
| `status` | Muestra config + impresoras detectadas |
| `unpair` | Borra la config local (tras confirmación) |
| `version` | Imprime la versión |

`run` sirve para los dos modos: cuando lo arranca el Service Control
Manager de Windows el binario lo detecta solo y se comporta como
servicio (sin consola, log a archivo); cuando lo escribes tú en una
consola se comporta como siempre.

Flags globales:

| Flag | Default | Uso |
|---|---|---|
| `-backend URL` | URL de Railway prod | Sobrescribe el backend (testing, preview deploys) |

## Mantener el agent corriendo al reiniciar la computadora

### Opción A — Servicio de Windows (RECOMENDADA)

Es la única opción que cumple las tres cosas que un restaurante necesita:

- **Arranca con el equipo**, sin que nadie inicie sesión en Windows.
  Si la máquina se reinicia de madrugada, a la hora de abrir ya está
  imprimiendo.
- **Se reinicia solo si se cae**: Windows lo revive a los 5 s, 5 s y
  luego cada 30 s, indefinidamente.
- **No hay ventana que cerrar** por accidente.

#### Instalación

Abre PowerShell **como administrador** (click DERECHO en el menú Inicio
→ "Terminal (Administrador)" o "Windows PowerShell (Administrador)") y
ejecuta:

```powershell
cd C:\NovaSoftAgent
.\novasoft-agent.exe service install
.\novasoft-agent.exe service start
```

Salida esperada de `service install`:

```
✓ Servicio "NovaSoftPrintAgent" instalado.

  Nombre:      NovaSoftPrintAgent (NovaSoft Print Agent)
  Ejecutable:  C:\NovaSoftAgent\novasoft-agent.exe run
  Cuenta:      LocalSystem
  Arranque:    Automático (con el equipo, sin necesidad de iniciar sesión)
  Recuperación: reinicio a los 5 s, 5 s y luego cada 30 s
  Config:      C:\ProgramData\NovaSoftAgent\config.json
  Log:         C:\ProgramData\NovaSoftAgent\logs\agent.log
```

#### Confirmar que quedó bien

```powershell
.\novasoft-agent.exe service status
```

Tienes que ver `Estado: CORRIENDO` y `Arranque: Automático`. Con las
herramientas de Windows es lo mismo:

```powershell
sc query NovaSoftPrintAgent      # STATE debe decir 4  RUNNING
sc qc NovaSoftPrintAgent         # START_TYPE debe decir 2  AUTO_START
sc qfailure NovaSoftPrintAgent   # las tres acciones de reinicio
```

#### Los demás comandos

| Comando | Qué hace | ¿Admin? |
|---|---|---|
| `service install` | Crea el servicio (auto + reinicio ante fallo) | Sí |
| `service uninstall` | Lo detiene y lo elimina | Sí |
| `service start` | Lo arranca | Sí |
| `service stop` | Lo detiene | Sí |
| `service status` | Estado, cuenta, recuperación, ruta del log | No |

#### Si `service install` falla

- **"se requieren permisos de administrador…"** — no abriste PowerShell
  como administrador. El propio mensaje trae los pasos; repítelos.
- **"el servicio ... ya está instalado"** — normal si estás
  actualizando. Ejecuta `service uninstall` y vuelve a instalar.
- **El servicio arranca y se detiene solo** — casi siempre es que no
  hay emparejamiento todavía, o que la impresora no está encendida. Mira
  `C:\ProgramData\NovaSoftAgent\logs\agent.log`; la primera línea de
  error te lo dice. Empareja (`pair`) y luego `service start`.

### Opción B — Acceso directo al inicio (solo si no puedes usar el servicio)

Sirve si el equipo no te deja instalar servicios. **Tiene las tres
limitaciones que la opción A resuelve**: no arranca sin login, nadie lo
levanta si se cae, y cerrar la ventana lo mata.

1. `Win + R` → `shell:startup` → Enter.
2. Crea un acceso directo a `novasoft-agent.exe` en esa carpeta.
3. Click derecho → Propiedades → Destino → agrégale ` run` al final,
   quedando así:
   ```
   C:\NovaSoftAgent\novasoft-agent.exe run
   ```
4. Aplicar. La próxima vez que el usuario inicie sesión, el agent
   arranca automáticamente.

## Dónde viven la config y los logs

| Qué | Ruta |
|---|---|
| Config (token) | `C:\ProgramData\NovaSoftAgent\config.json` |
| Log del agent | `C:\ProgramData\NovaSoftAgent\logs\agent.log` |
| Logs rotados | `agent.log.1` … `agent.log.3` (5 MB cada uno) |
| Eventos del servicio | Visor de eventos → Registros de Windows → Aplicación → origen `NovaSoftPrintAgent` |

`C:\ProgramData` es un directorio **de máquina**, no de usuario. Es
deliberado: el servicio corre como `LocalSystem` y esa cuenta no ve el
`%APPDATA%` de la persona que hizo `pair`. Si dejáramos el config donde
estaba antes (`%APPDATA%\NovaSoftPrintAgent`), el servicio nunca lo
encontraría.

**Si vienes de una versión anterior (≤ v0.2.2) no tienes que volver a
emparejar**: la primera vez que corras cualquier comando —y
explícitamente durante `service install`— el agent copia el config viejo
a la ruta nueva. El archivo viejo se deja donde está, por si necesitas
volver a la versión anterior.

## Solución de problemas

### "no hay configuración — ejecuta 'pair' primero"

El agent no encuentra `config.json` en
`C:\ProgramData\NovaSoftAgent\`. Causas:
- No has hecho `pair` todavía.
- Borraste el config con `unpair`.
- Hiciste `pair` con una versión ≤ v0.2.2 y el archivo viejo quedó en
  `%APPDATA%\NovaSoftPrintAgent\config.json`. El agent lo migra solo,
  pero solo puede verlo el usuario que hizo el `pair`: corre
  `.\novasoft-agent.exe service install` (o cualquier comando) con **ese
  usuario** y se copiará a la ruta nueva.

### El servicio está instalado pero no imprime

Por orden:

1. `.\novasoft-agent.exe service status` — ¿dice `CORRIENDO`?
   - Si dice `DETENIDO`, arráncalo: `service start` (como administrador).
2. Abre `C:\ProgramData\NovaSoftAgent\logs\agent.log` con el Bloc de
   notas y mira las últimas líneas. Ahí está el motivo real.
3. Si el log dice que no encuentra impresoras: enciende la impresora y
   reinicia el servicio (`service stop` y `service start`).
4. Visor de eventos → Registros de Windows → Aplicación, filtra por
   origen `NovaSoftPrintAgent`: ahí quedan arranques, paradas y errores
   fatales.

### El servicio no ve la impresora, pero `run` a mano sí

El servicio corre como `LocalSystem`. Esa cuenta ve las impresoras
instaladas **para todo el equipo**, pero NO las que un usuario agregó
solo para su perfil (típico de impresoras de red agregadas desde
"Agregar impresora" con una sesión iniciada).

Solución: reinstala la impresora como impresora del equipo (con el
driver del fabricante, conectada por USB, es lo normal), o comparte la
impresora de red a nivel máquina.

### "no hay impresoras instaladas en Windows"

- Ve a Configuración → Bluetooth y dispositivos → Impresoras.
- Instala el driver de tu impresora.
- Imprime una página de prueba desde Windows. Si falla ahí, el agent no
  puede arreglarlo — es un problema de driver/hardware.

### "Error de conexión con el servidor"

- Revisa que tu internet funcione (abre novasoft.mx en el navegador).
- Revisa que tu firewall no bloquee WebSockets salientes al puerto 443.
- Espera unos segundos — el agent reintenta con backoff exponencial
  automáticamente. Si el mensaje persiste tras 1 minuto, verifica el
  status del backend en https://status.novasoft.mx.

### "El ticket sale con basura / caracteres raros"

El driver de la impresora podría estar intentando interpretar los bytes
ESC/POS como texto. Verifica en Windows:

1. Configuración → Impresoras → [tu impresora] → Preferencias de
   impresión.
2. Busca "Impresión directa" o "Raw" y actívala.
3. Si tu driver no lo soporta, puede que necesites un driver genérico
   "ESC/POS USB" en vez del específico del fabricante.

### "El ticket sale cortado a la mitad"

- Verifica que el rollo no esté al final.
- Verifica que la impresora tenga tapa cerrada correctamente.
- El cortador puede estar atascado — abre y cierra la tapa.

### "El agent se desconecta cada pocos segundos"

Normal si la red es inestable. El agent reconecta automáticamente con
backoff exponencial. Si persiste, revisa:

- ¿Tu router tiene un timeout agresivo para conexiones persistentes?
- ¿Hay un proxy corporativo entre la caja e internet? Puede bloquear
  WebSockets.

### "Agregué una segunda impresora y no la detecta"

En Fase 1a, el agent usa la primera impresora térmica detectada.
Para tener impresoras distintas por estación (cuenta vs. cocina), espera
Fase 2+ o instala un segundo agent en otra computadora.

## Reinstalar / desemparejar

Si tienes que reinstalar o mover el agent a otra computadora:

```
.\novasoft-agent.exe unpair
```

En NovaSoft → Impresión → 🗑️ junto al agent viejo. Luego repite los
pasos 2–5 desde la computadora nueva.

## Privacidad

El agent:
- **NO manda** datos de venta a NovaSoft.
- Solo mantiene una conexión SSE saliente para recibir jobs y reportar
  si imprimieron correctamente.
- El token local vive en `C:\ProgramData\NovaSoftAgent\config.json`, con
  los permisos que hereda de `C:\ProgramData`: control total para SYSTEM
  y Administradores, **solo lectura** para el resto de usuarios del
  equipo. Es un cambio respecto a versiones ≤ v0.2.2, donde el archivo
  estaba en `%APPDATA%` y solo lo veía un usuario; el precio de que el
  servicio (LocalSystem) pueda leerlo. En una caja de restaurante, donde
  todos los usuarios son del negocio, es una compensación aceptable —
  pero si compartes ese equipo con terceros, tenlo en cuenta.
- Si pierdes acceso al equipo, desempareja desde NovaSoft (🗑️) — eso
  invalida el token remoto.

## Soporte

- Email: contacto@novasoft.mx
- Issues: https://github.com/Richi2489/novasoft-print-agent/issues
