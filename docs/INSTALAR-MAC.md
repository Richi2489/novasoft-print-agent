# Instalar el agente de impresión de NovaSoft en una Mac

Guía paso a paso para dejar imprimiendo tickets en una **impresora térmica
conectada a una Mac** (caja del restaurante). No necesitas saber de
computadoras: copia y pega los comandos tal cual.

Tiempo: 15 minutos. Necesitas: la Mac con internet, la impresora térmica
encendida y con papel, y alguien con acceso a NovaSoft como ADMIN o GERENTE.

---

## 0. Antes de empezar: ¿qué Mac tengo?

Menú  (la manzana, arriba a la izquierda) → **Acerca de esta Mac**.

Fíjate en dos cosas:

- **Versión de macOS** (ej. *macOS Mojave 10.14.6*).
- **Procesador / Chip**: dice *Intel Core i5* (u otro Intel) o *Apple M1/M2/M3…*.

| Tu Mac dice | Archivo que se usa | ¿Funciona? |
|---|---|---|
| Intel + macOS 10.13 High Sierra o más nuevo (Mojave 10.14 incluido) | `novasoft-agent-mac-intel` | Sí |
| Apple M1/M2/M3 + macOS 12 o más nuevo | `novasoft-agent-mac-arm64` | Sí |
| Apple M1 + macOS 11 | `novasoft-agent-mac-intel` (vía Rosetta) | Sí |
| macOS 10.12 o más viejo | — | No |

No tienes que elegirlo tú: el instalador lo detecta solo.

---

## 1. Conecta la impresora

### Si es USB (lo normal)

1. Conecta el cable USB a la Mac y enciende la impresora.
2. Abre **Preferencias del Sistema** → **Impresoras y escáneres**.
3. Si la térmica ya aparece en la lista de la izquierda, pasa al paso 2 de
   esta guía.
4. Si no aparece, pulsa **+** (abajo a la izquierda). En la pestaña
   **Por omisión** debe salir la impresora (el nombre suele ser algo como
   *POS-80*, *Printer POS-80*, *XP-80C*, *GTP801*, *Thermal Printer*…).
   Selecciónala.
5. En **Usar:** elige **Seleccionar software…** → busca **Generic** → elige
   **Impresora PostScript genérica** (o "Generic PostScript Printer").
   Si el fabricante te dio un driver para Mac, también sirve.
   *No importa cuál elijas: el agente le manda los tickets "en crudo" y el
   driver no interviene.*
6. Pulsa **Agregar**. Si pregunta por opciones, **Aceptar**.

> Si en el paso 4 no aparece nada, no te preocupes: el instalador del paso 3
> puede crearla él solo (te preguntará; dile que sí).

**Nombre de la cola.** Para saber el nombre exacto con el que la Mac conoce
a la impresora, abre Terminal (paso 3) y escribe:

```
lpstat -p
```

Sale algo como `printer POS_80 is idle…`. El nombre es `POS_80` (con guiones
bajos). El instalador te los muestra con número, así que normalmente no
necesitas esto.

### Si es de red (cable Ethernet al módem/switch) — alternativa

No hay que agregarla en la Mac. Sólo necesitas su **IP** (ej.
`192.168.1.50`). Casi todas las térmicas la imprimen si las enciendes
manteniendo presionado el botón **FEED** (sale una hoja de configuración).
El agente le habla directo al puerto 9100.

---

## 2. Genera el código en NovaSoft

1. Entra a NovaSoft (ADMIN o GERENTE) → **Configuración → Impresoras →
   Agregar impresora**.
2. Ponle nombre ("Caja Feria") → **Generar código**.
3. Deja a la vista el código, tipo `ABCD-1234-WXYZ`. **Dura 10 minutos.**

---

## 3. Descarga e instala el agente

1. Descarga **`NovaSoftAgent-mac.zip`** (te lo mandan por correo/WhatsApp o
   desde la liga que te dé soporte) y haz **doble clic** para
   descomprimirlo. Queda la carpeta **NovaSoftAgent-mac** en *Descargas*.
2. Abre **Terminal**: Finder → **Aplicaciones → Utilidades → Terminal**
   (o pulsa ⌘ + espacio, escribe `Terminal` y Enter).
3. Copia y pega esta línea y pulsa **Enter**:

   ```
   cd ~/Downloads/NovaSoftAgent-mac && bash install-mac.sh
   ```

   > Si la carpeta quedó en otro lugar: escribe `cd ` (con espacio),
   > **arrastra la carpeta** a la ventana de Terminal, Enter, y luego
   > `bash install-mac.sh`.

4. El instalador te va preguntando:
   - **Código**: escribe el de NovaSoft y Enter. Si está mal, ya se usó o
     venció, te lo dice y te deja escribir otro.
   - **Impresora**: te muestra una lista numerada. Escribe el **número** de
     la térmica y Enter. Si es de red, escribe su **IP**
     (ej. `192.168.1.50`). Si no aparece y es USB, escribe **U** y te ayuda
     a agregarla (puede pedirte la **contraseña de la Mac**; al teclearla no
     se ve nada, es normal).
   - **¿Imprimir prueba?**: Enter (sí). Debe salir un ticket que dice
     *NovaSoft — Prueba de impresión*.
5. Al final dice **Listo ✓**. Ya puedes cerrar la Terminal.

### Si macOS dice que "no se puede abrir" o "desarrollador no identificado"

El agente no está firmado por Apple. El instalador ya le quita el bloqueo,
pero si aun así aparece el aviso:

- **Mojave (10.14) / Catalina**: **Preferencias del Sistema → Seguridad y
  privacidad → General** → abajo dice *"novasoft-agent" se bloqueó…* →
  **Abrir de todos modos**. Vuelve a correr
  `bash install-mac.sh` (los datos ya escritos se conservan).
- **macOS 13 o más nuevo**: **Configuración del Sistema → Privacidad y
  seguridad** → bajar → **Abrir de todos modos**.

---

## 4. Comprueba que funciona

1. En NovaSoft, **Configuración → Impresoras**: la impresora debe aparecer
   **En línea** en unos segundos. Desde ahí puedes mandar la **prueba de
   NovaSoft** (imprime un ticket que viaja por internet hasta la Mac).
2. Manda una precuenta desde el POS: debe salir en la térmica.

**¿Cómo sé que el agente está corriendo?** En Terminal:

```
~/Library/Application\ Support/NovaSoftPrintAgent/novasoft-agent status
```

Debe decir `Servicio: corriendo (launchd, PID …) ✓` y `Agent ID: …`.

El agente **arranca solo** cada vez que se inicia sesión en la Mac y se
reinicia solo si se cierra. No hay ventana que dejar abierta.

> **Importante**: arranca al **iniciar sesión** del usuario, no al prender
> la Mac. Si la Mac se reinicia y se queda en la pantalla de contraseña,
> NO imprime hasta que alguien entre. Recomendado: Preferencias del Sistema
> → Usuarios y grupos → Opciones → **Inicio de sesión automático** con el
> usuario de la caja. Y en **Economizador** que la Mac **no entre en
> reposo** (la pantalla sí se puede apagar).

---

## 5. Problemas comunes

| Síntoma | Qué hacer |
|---|---|
| La prueba local no sale y la impresora es USB | Revisa cable y papel. En Preferencias del Sistema → Impresoras y escáneres, que la cola no esté en **pausa** (botón *Reanudar*). Prueba otra vez: `~/Library/Application\ Support/NovaSoftPrintAgent/novasoft-agent test-print` |
| Sale basura o símbolos raros | La cola se agregó con un driver que transforma el texto. El agente manda "raw", pero si persiste, borra la impresora en Preferencias y deja que el instalador la cree (opción **U**). |
| Imprime en la impresora de oficina en vez de la térmica | Fija la térmica: `…/novasoft-agent printer list` y luego `…/novasoft-agent printer set POS_80` (el nombre que salga). Después: `…/novasoft-agent service start`. |
| La USB no aparece en ninguna lista | Algunas térmicas baratas se conectan como "puerto serie" y no como impresora; con esas no funciona por USB. Usa la conexión de red si la tiene, o una térmica distinta. |
| De red: "connection refused" / "timeout" | La IP cambió o la Mac está en otra red. Imprime la hoja de configuración (FEED) y repite `printer set <IP nueva>`. |
| NovaSoft la muestra "Desconectada" | Mira el log: `tail -50 ~/Library/Application\ Support/NovaSoftPrintAgent/logs/agent.log`. Si dice "no hay configuración", empareja otra vez con `bash install-mac.sh CODIGO`. |

Archivos del agente (por si soporte los pide):

- Programa y config: `~/Library/Application Support/NovaSoftPrintAgent/`
  (`config.json` contiene el token: **no lo compartas**).
- Log: `~/Library/Application Support/NovaSoftPrintAgent/logs/agent.log`
- Arranque automático: `~/Library/LaunchAgents/mx.novasoft.printagent.plist`

Comandos útiles (anteponer `~/Library/Application\ Support/NovaSoftPrintAgent/`):

```
novasoft-agent status            # estado general
novasoft-agent printer list      # impresoras de la Mac
novasoft-agent printer set X     # fijar impresora (cola o IP:9100)
novasoft-agent test-print        # ticket de prueba local
novasoft-agent service stop      # detener
novasoft-agent service start     # arrancar
```

---

## 6. Desinstalar

En Terminal, dentro de la carpeta descomprimida:

```
cd ~/Downloads/NovaSoftAgent-mac && bash uninstall-mac.sh
```

Quita el agente y su arranque automático; conserva el emparejamiento por si
lo vuelves a instalar. Para borrar todo (incluido el token):

```
bash uninstall-mac.sh --todo
```

y en NovaSoft borra la impresora de **Configuración → Impresoras**.
La impresora agregada en la Mac no se toca.

---

## Notas técnicas (soporte)

- Binario Intel compilado con **Go 1.20.14** (último Go que arranca en
  macOS 10.13/10.14; Go 1.21+ exige 10.15 y Go 1.25+ exige 12). El de
  Apple Silicon usa el Go del repo (macOS 12+). Build: `build\build-mac.ps1`.
- USB: el agente envía con `lp -d <cola> -o raw` (CUPS sin filtros).
  La opción **U** crea la cola con `lpadmin -p NovaSoft_POS -E -v usb://… -m raw`.
- Red: socket TCP directo a `IP:9100` (`printer set tcp://IP:9100`, o la IP sola).
- Sin servicio de sistema: LaunchAgent de usuario (`RunAtLoad` + `KeepAlive`),
  sin sudo. `launchctl print gui/$(id -u)/mx.novasoft.printagent` para diagnóstico.
- El binario no está firmado ni notarizado por Apple: el instalador quita el
  atributo `com.apple.quarantine`.
