// Package ipc define el contrato entre el servicio del agent (servidor)
// y el tray icon u otros clientes (CLI status).
//
// Transporte:
//   - Windows: named pipe `\\.\pipe\NovaSoftAgent` con SDDL que permite
//     a Authenticated Users conectar al servidor LocalSystem.
//   - Otros OS: no implementado en v0.3.0 (el tray es Windows-only).
//
// Protocolo:
//   - JSON line-delimited (un Request por línea, una Response por línea).
//   - Cada conexión: 1 request → 1 response → cierre.
//   - Sin streaming, sin keepalive (las conexiones son cortas).
//
// Por qué named pipe y no HTTP localhost:
//   - El servicio corre en sesión 0 (LocalSystem); HTTP requeriría
//     levantar un puerto que es visible para todos los procesos del
//     sistema. Named pipes con SDDL restringen el acceso por ACL.
//   - Sin colisión de puertos.
//   - Latencia local equivalente.
package ipc

import "encoding/json"

// PipeName es la ruta canónica del named pipe en Windows.
// En named pipes, el path es global: cualquier cliente con permisos
// puede abrir `\\.\pipe\NovaSoftAgent` y hablar con el servidor.
const PipeName = `\\.\pipe\NovaSoftAgent`

// Comandos soportados. Strings en lugar de iota porque viajan en JSON.
const (
	// CmdGetStatus → StatusResponse con conectado/desconectado, agent_id,
	// backend_url, version, last_job_at.
	CmdGetStatus = "GET_STATUS"

	// CmdRestartConnection fuerza al runner a cerrar el SSE actual y
	// reabrir la conexión. Útil si el agent quedó en backoff largo y
	// el admin sabe que la red ya volvió.
	CmdRestartConnection = "RESTART_CONNECTION"

	// CmdPair empareja el agent contra un restaurant usando el código
	// generado por el wizard NovaSoft. El payload del Request lleva
	// PairRequest (code + backend_url opcional). El servidor:
	//   1. Valida + normaliza el código (NormalizeCode acepta con/sin
	//      guiones, mayús/minús).
	//   2. Hace POST {backend}/printing/agents/pair (handshake).
	//   3. Guarda config.json en %PROGRAMDATA%\NovaSoft\.
	//   4. Notifica al runner para que reinicie el SSE con el token
	//      nuevo (sin reiniciar el servicio Windows).
	//
	// Re-pair sobre un agent ya emparejado: sobrescribe el config viejo;
	// el HMAC anterior queda colgado del lado servidor (admin debe
	// borrarlo manual desde NovaOps si quiere limpiar).
	CmdPair = "PAIR"
)

// Request es la unidad de mensaje entrante al servidor IPC.
//
// Payload es opcional — comandos sin parámetros (GetStatus,
// RestartConnection) lo dejan nil. Comandos con parámetros (Pair)
// llevan un sub-tipo serializado a JSON ahí.
type Request struct {
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PairRequest es el payload del Request cuando Command == CmdPair.
// Code es obligatorio. BackendURL es opcional — vacío usa el default
// del servidor (https://api.novasoft.mx).
type PairRequest struct {
	Code       string `json:"code"`
	BackendURL string `json:"backend_url,omitempty"`
}

// PairResponseData es el payload de Data en la Response cuando el
// pair fue exitoso. Refleja un subset del PairResponse del backend
// que es útil para el cliente (tray) — el agent_token completo NO
// se incluye, queda guardado en config.json del lado del servidor.
type PairResponseData struct {
	AgentID string `json:"agent_id"`
}

// Response es la unidad de mensaje saliente. Si Error está set, OK es
// false y Data se ignora. Si OK es true, Data tiene el payload del
// comando específico.
type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// StatusResponse es el payload de Data cuando el comando es CmdGetStatus.
//
// Campos:
//   - Connected: true si el SSE está abierto y recibió al menos el
//     handshake. False durante el backoff de reconexión.
//   - AgentID: UUID del agent emparejado, vacío si no hay config.
//   - BackendURL: URL del backend, p. ej. "https://api.novasoft.mx".
//   - Version: versión del binary del agent.
//   - LastJobAt: timestamp RFC3339 del último job procesado, o vacío
//     si nunca se procesó uno desde el arranque.
//   - PrinterName: nombre de la impresora seleccionada, vacío si el
//     runner aún no terminó la fase de discovery.
type StatusResponse struct {
	Connected   bool   `json:"connected"`
	AgentID     string `json:"agent_id,omitempty"`
	BackendURL  string `json:"backend_url,omitempty"`
	Version     string `json:"version,omitempty"`
	LastJobAt   string `json:"last_job_at,omitempty"`
	PrinterName string `json:"printer_name,omitempty"`
}
