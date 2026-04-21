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
│   ├── config/             # persistencia JSON (APPDATA / XDG)
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

Desde v0.2.0: ya NO usamos `github.com/gorilla/websocket` — SSE vive
en `net/http` del stdlib.

Todas con `CGO_ENABLED=0` — el binary resulta portable single-file sin
dependencias runtime en la máquina del cliente.

## Licencia

Propietaria. Ver [`LICENSE.txt`](LICENSE.txt).

Uso restringido a clientes NovaSoft licenciados. Para licensing:
contacto@novasoft.mx.
