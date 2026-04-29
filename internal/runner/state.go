// State holder thread-safe que el runner publica para que el IPC server
// (y eventualmente el tray) pueda consultar conectado/desconectado, etc.
//
// El State vive como singleton package-level porque hay un solo runner
// activo por proceso (servicio o CLI). Acceso vía RWMutex para que el
// IPC handler pueda leer sin bloquear writes del runner.
package runner

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/pairing"
	"github.com/Richi2489/novasoft-print-agent/internal/sse"
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

// ────────────────────────────────────────────────────────────────────
// Pair flow — invocado desde el IPC server cuando recibe CmdPair.
// ────────────────────────────────────────────────────────────────────

// defaultBackend se usa cuando el cliente IPC no provee BackendURL.
// El cmd/agent/main.go lo asigna en init() con el mismo valor que
// DefaultBackendURL. Sin esto el pair desde el tray fallaría con
// "BackendURL vacío".
var defaultBackend atomic.Value // string

// SetDefaultBackend lo invoca main() al arranque. Single source of
// truth para el backend default.
func SetDefaultBackend(url string) {
	defaultBackend.Store(url)
}

func getDefaultBackend() string {
	v := defaultBackend.Load()
	if v == nil {
		return "https://api.novasoft.mx"
	}
	return v.(string)
}

// configChangedCh señala al runner loop que el config cambió y debe
// recargarlo. Buffer 1 porque múltiples notifications colapsan en una
// sola re-evaluación (el loop solo necesita saber "hubo cambio").
var configChangedCh = make(chan struct{}, 1)

// NotifyConfigChanged señala al runner que recargue config + reinicie
// SSE. Llamado por PairAndApply después de un pair exitoso.
func NotifyConfigChanged() {
	select {
	case configChangedCh <- struct{}{}:
	default:
	}
}

// drainConfigChanged consume cualquier señal pendiente. El runner
// la usa al iniciar cada iteración del loop para no quedarse con
// señales viejas que disparen reset spurious.
func drainConfigChanged() {
	select {
	case <-configChangedCh:
	default:
	}
}

// waitForConfigChanged bloquea hasta que llegue una señal o el ctx
// se cancele. Devuelve true si la señal llegó, false si ctx murió.
// Usado por el runner cuando no hay config y necesita esperar a
// que alguien empareje vía IPC.
func waitForConfigChanged(stop <-chan struct{}) bool {
	select {
	case <-configChangedCh:
		return true
	case <-stop:
		return false
	}
}

// ────────────────────────────────────────────────────────────────────
// Current SSE client — usado por el IPC para Restart.
// ────────────────────────────────────────────────────────────────────

var currentClient atomic.Pointer[sse.Client]

// setCurrentClient publica el cliente activo. El runner lo llama al
// abrir cada conexión SSE y con nil al cerrar. Internal — no exportar.
func setCurrentClient(c *sse.Client) {
	currentClient.Store(c)
}

// RequestRestartCurrent dispara restart del SSE activo si hay uno.
// Devuelve error amigable si no hay conexión (ej. agent sin config).
// Llamado desde el IPC handler de CmdRestartConnection.
func RequestRestartCurrent() error {
	c := currentClient.Load()
	if c == nil {
		return errors.New("no hay conexión activa — el agent no está emparejado o el SSE no se ha abierto todavía")
	}
	c.RequestRestart()
	return nil
}

// ────────────────────────────────────────────────────────────────────
// Pair flow — handshake + save config + signal runner.
// ────────────────────────────────────────────────────────────────────

// PairAndApply ejecuta el handshake de pair contra el backend, guarda
// el config resultante, y señala al runner para que reinicie el SSE
// con el token nuevo. Llamado desde el IPC handler de CmdPair.
//
// backendURL vacío → usa el default (api.novasoft.mx).
//
// Errores comunes (devueltos como string para que el cliente IPC los
// pase al user):
//   - "código vacío" — el caller debe validar antes pero defensa.
//   - "código inválido" — backend devolvió 404.
//   - "código expirado" — backend devolvió 410.
//   - "no pude conectar con el servidor" — DNS/red.
//
// Re-pair sobre config existente: sobrescribe atómicamente. El SSE
// activo se cierra por el configChanged signal y el nuevo se abre
// con el token nuevo. El HMAC viejo queda huérfano del lado backend
// hasta que admin lo borre desde NovaOps.
func PairAndApply(code, backendURL string) (*pairing.PairResponse, error) {
	if backendURL == "" {
		backendURL = getDefaultBackend()
	}

	resp, err := pairing.Pair(backendURL, code, AgentVersion)
	if err != nil {
		return nil, err
	}

	cfg := &config.Config{
		AgentID:      resp.AgentID,
		AgentToken:   resp.AgentToken,
		WebsocketURL: resp.WebsocketURL,
		BackendURL:   backendURL,
	}
	if err := config.Save(cfg); err != nil {
		return nil, fmt.Errorf("guardando config: %w", err)
	}

	log.Printf("✓ Pair exitoso desde IPC: agent_id=%s", resp.AgentID)
	NotifyConfigChanged()
	return resp, nil
}
