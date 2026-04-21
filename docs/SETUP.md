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

En la computadora de la caja, abre PowerShell (click derecho al menú
Inicio → "Windows PowerShell") y ve a la carpeta del agent:

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
  Config:   C:\Users\<tu_usuario>\AppData\Roaming\NovaSoftPrintAgent\config.json

Ahora ejecuta:
  novasoft-agent.exe run
```

En el wizard de NovaSoft verás que avanzó al **Paso 3** automáticamente.

## 4 — Arranca el Agent

Con el emparejamiento hecho, lanza el modo de operación:

```
.\novasoft-agent.exe run
```

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
| `run` | Arranca el loop principal — escucha jobs |
| `status` | Muestra config + impresoras detectadas |
| `unpair` | Borra la config local (tras confirmación) |
| `version` | Imprime la versión |

Flags globales:

| Flag | Default | Uso |
|---|---|---|
| `-backend URL` | URL de Railway prod | Sobrescribe el backend (testing, preview deploys) |

## Mantener el agent corriendo al reiniciar la computadora

**Opción A — Acceso directo al inicio** (sencillo):

1. `Win + R` → `shell:startup` → Enter.
2. Crea un acceso directo a `novasoft-agent.exe` en esa carpeta.
3. Click derecho → Propiedades → Destino → agrégale ` run` al final,
   quedando así:
   ```
   C:\NovaSoftAgent\novasoft-agent.exe run
   ```
4. Aplicar. La próxima vez que el usuario inicie sesión, el agent
   arranca automáticamente.

**Opción B — Windows Service** llegará en un sprint posterior.

## Solución de problemas

### "no hay configuración — ejecuta 'pair' primero"

El agent no encuentra `config.json`. Causas:
- No has hecho `pair` todavía.
- Borraste el config con `unpair`.
- Estás ejecutando el agent con un usuario de Windows distinto al que
  hizo el `pair` (cada usuario tiene su propio `%APPDATA%`).

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
- Solo mantiene una conexión WebSocket para recibir jobs y reportar si
  imprimieron correctamente.
- El token local (`config.json`) tiene permisos `0600` — solo tu usuario
  de Windows lo lee.
- Si pierdes acceso al equipo, desempareja desde NovaSoft (🗑️) — eso
  invalida el token remoto.

## Soporte

- Email: contacto@novasoft.mx
- Issues: https://github.com/Richi2489/novasoft-print-agent/issues
