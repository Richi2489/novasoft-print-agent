// State holder thread-safe que el runner publica para que el IPC server
// (y eventualmente el tray) pueda consultar conectado/desconectado, etc.
//
// El State vive como singleton package-level porque hay un solo runner
// activo por proceso (servicio o CLI). Acceso vía RWMutex para que el
// IPC handler pueda leer sin bloquear writes del runner.
package runner

import (
	"sync"
	"time"
)

// state guarda lo que el IPC consulta — no más, no menos. Si en el
// futuro se agrega más telemetría, agregar aquí.
type stateSnapshot struct {
	connected   bool
	agentID     string
	backendURL  string
	printerName string
	lastJobAt   time.Time
}

var (
	stateMu  sync.RWMutex
	curState stateSnapshot
)

// SetConnected actualiza el flag de conexión SSE. Lo invoca el runner
// desde los callbacks del cliente SSE (OnConnected / OnDisconnected).
func SetConnected(b bool) {
	stateMu.Lock()
	defer stateMu.Unlock()
	curState.connected = b
}

// SetIdentity guarda agent_id + backend_url al cargar config — antes
// de abrir SSE. Permite al IPC reportar identity aunque el SSE aún no
// haya conectado.
func SetIdentity(agentID, backendURL string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	curState.agentID = agentID
	curState.backendURL = backendURL
}

// SetPrinter registra el nombre de la impresora seleccionada para que
// el tray pueda mostrarla al usuario.
func SetPrinter(name string) {
	stateMu.Lock()
	defer stateMu.Unlock()
	curState.printerName = name
}

// RecordJob marca un job procesado (printed o failed) — el tray usa
// este timestamp para mostrar "Última actividad: hace X min" en el
// tooltip o en el menú.
func RecordJob() {
	stateMu.Lock()
	defer stateMu.Unlock()
	curState.lastJobAt = time.Now()
}

// Snapshot devuelve una copia del estado actual. Sin race contra
// writes del runner.
func Snapshot() (connected bool, agentID, backendURL, printerName string, lastJobAt time.Time) {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return curState.connected, curState.agentID, curState.backendURL, curState.printerName, curState.lastJobAt
}

// Reset limpia el estado — útil para tests o si el runner se reinicia
// dentro del mismo proceso.
func Reset() {
	stateMu.Lock()
	defer stateMu.Unlock()
	curState = stateSnapshot{}
}
