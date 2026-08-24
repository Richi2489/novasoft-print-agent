# novasoft-print-agent

Agent de impresión térmica ESC/POS para **NovaSoft POS**.

Binary Windows que empareja la caja del restaurante con el backend
cloud de NovaSoft y convierte los jobs de impresión declarativos que
recibe por Server-Sent Events en bytes ESC/POS para tu impresora
térmica 80 mm.

> **v0.2.0** (2026-04-21) pivoteó de WebSocket a SSE porque Railway +
> Fastly strippean el header `Upgrade: websocket` antes de llegar al
> origin. SSE es HTTP/1.1 chunked puro y pasa por cualquier CDN sin
> config especial. Ver `ADR-015` del backend.

## ¿Qué es NovaSoft?

NovaSoft es un POS + plataforma multi-tenant para restaurantes en la
nube. Ver [novasoft.mx](https://novasoft.mx).

## Cómo funciona

```
NovaSoft POS (Next.js)
      │ 1. click "Imprimir cuenta"
      ▼
NovaSoft backend (FastAPI, Railway)
      │ 2. POST /printing/jobs
      │ 3. publica vía SSE (text/event-stream)
      ▼
novasoft-agent.exe (esta repo)
      │ 4. convierte payload → ESC/POS
      │ 5. POST /printing/agents/jobs/{id}/result
      ▼
Impresora térmica 80 mm
      │ 5. papel sale con la cuenta
      ▼
🧾
```

## Empezar

Ver [`docs/SETUP.md`](docs/SETUP.md) para la guía paso a paso
(descarga, emparejamiento, prueba).

Modelos certificados en [`docs/CERTIFIED_PRINTERS.md`](docs/CERTIFIED_PRINTERS.md).

## Cómo se ejecuta

Desde **v0.3.0** la forma recomendada es como **servicio de Windows**:
arranca con el equipo sin que nadie inicie sesión, no tiene ventana que
cerrar, y Windows lo reinicia solo si se cae (5 s, 5 s, luego cada 30 s).

En PowerShell **como administrador**:

```powershell
cd C:\NovaSoftAgent
.\novasoft-agent.exe pair              # una sola vez
.\novasoft-agent.exe service install   # auto-arranque + reinicio ante fallo
.\novasoft-agent.exe service start
.\novasoft-agent.exe service status    # verificar (no requiere admin)
```

Modo manual, para diagnóstico, sigue funcionando igual que siempre:

```powershell
.\novasoft-agent.exe run               # corre en esta consola, Ctrl-C para salir
```

`run` es el mismo subcomando en ambos casos: el binario detecta con
`svc.IsWindowsService()` si lo arrancó el Service Control Manager y
elige el modo. Así no se puede dar el caso de instalar el servicio
apuntando a un modo y probar a mano el otro.

| Ruta | Qué hay |
|---|---|
| `C:\ProgramData\NovaSoftAgent\config.json` | Emparejamiento (token) |
| `C:\ProgramData\NovaSoftAgent\logs\agent.log` | Log, rotado a 5 MB × 4 |
| Visor de eventos → Aplicación → `NovaSoftPrintAgent` | Arranque, parada, errores fatales |

La config vive en `%PROGRAMDATA%` (máquina) y no en `%APPDATA%`
(usuario) porque el servicio corre como `LocalSystem`, que tiene otro
`%APPDATA%` y nunca encontraría el config del usuario que hizo `pair`.
Los agents emparejados con versiones anteriores se migran solos — ver
`internal/config` y `docs/SETUP.md`.

## Desarrollo

Requiere **Go 1.22+**.

```bash
# Compilar
.\build\build-windows.ps1

# O directo:
go build -o dist\windows\novasoft-agent.exe ./cmd/agent
```

### Estructura

```
novasoft-print-agent/
├── cmd/agent/              # main — orquestación de subcomandos
├── internal/
│   ├── config/             # persistencia JSON (PROGRAMDATA / XDG) + migración
│   ├── logging/            # log a archivo con rotación por tamaño
│   ├── winsvc/             # servicio de Windows: SCM, recovery, Event Log
│   │                       # (*_windows.go con build tag)
│   ├── pairing/            # handshake HTTP para obtener token
│   ├── sse/                # cliente SSE (net/http stdlib) + POST result
│   └── printer/            # discovery + send raw + ESC/POS converter
│                           # (discover/print *_windows.go con build tag)
├── pkg/escpos/             # constantes ESC/POS (reutilizable)
├── build/                  # scripts de build Windows
└── docs/                   # guías para admin + certified printers
```

### Dependencias externas

| Package | Propósito |
|---|---|
| `github.com/cenkalti/backoff/v4` | Backoff exponencial para reconexión |
| `github.com/alexbrainman/printer` | winspool wrapper (puro syscall, sin CGO) |
| `golang.org/x/sys/windows/svc` | Servicio de Windows: SCM, `mgr`, Event Log |

Desde v0.2.0: ya NO usamos `github.com/gorilla/websocket` — SSE vive
en `net/http` del stdlib.

Todas con `CGO_ENABLED=0` — el binary resulta portable single-file sin
dependencias runtime en la máquina del cliente.

## Licencia

Propietaria. Ver [`LICENSE.txt`](LICENSE.txt).

Uso restringido a clientes NovaSoft licenciados. Para licensing:
contacto@novasoft.mx.
