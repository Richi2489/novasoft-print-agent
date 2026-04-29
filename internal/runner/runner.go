// Package runner contiene el loop principal del agent (detect printer +
// SSE + dispatch de jobs). Vive separado de cmd/agent/main.go porque
// se invoca desde dos contextos:
//
//  1. CLI interactivo: `novasoft-agent.exe run` — desde main.cmdRun.
//  2. Servicio Windows: invocado por kardianos/service en main.program.
//
// El extracto evita duplicar la lógica entre los dos paths. Antes de
// v0.3.0, esta lógica vivía inline en cmdRun() y no había modo servicio.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/ipc"
	"github.com/Richi2489/novasoft-print-agent/internal/printer"
	"github.com/Richi2489/novasoft-print-agent/internal/sse"
)

// AgentVersion es el string de versión del binary que se reporta vía
// IPC al tray. main() lo asigna al inicio (es la misma var que se
// inyecta con -ldflags).
var AgentVersion = "0.3.0-dev"

// Run es el loop principal del agent. Vive durante todo el lifetime
// del servicio (o del CLI `run`) y soporta:
//
//   - Arranque sin config — se queda esperando que alguien ejecute pair
//     (vía CLI o vía tray > Emparejar). Usaba retornar error en v0.2.x;
//     desde v0.3.0 esperamos para que el flujo Camino C funcione: el
//     servicio se instala antes del primer pair sin morir.
//   - Re-pair en caliente — cuando el IPC PAIR succeeds, NotifyConfigChanged
//     señala el inner loop para cerrar el SSE viejo y reabrir con el
//     token nuevo. Sin reiniciar el servicio Windows.
//   - Restart explícito — IPC RESTART_CONNECTION llama RequestRestart()
//     en el cliente SSE actual; el inner loop reconecta inmediato.
//   - Reconnect con backoff — el sse.Client maneja desconexiones de red
//     internamente, no tocamos eso.
//
// Side-effects:
//   - Publica estado en runner.Snapshot() (consultado por IPC GetStatus).
//   - Levanta el IPC server en \\.\pipe\NovaSoftAgent una sola vez al
//     arranque — sobrevive re-pairs y re-conexiones.
//
// El IPC server se considera obligatorio en producción para que el tray
// pueda hablar con el servicio. Si falla al arrancar (pipe ya en uso,
// SDDL inválida, etc.), lo logueamos pero seguimos — la impresión core
// funciona sin IPC, solo el tray queda ciego.
func Run(ctx context.Context) error {
	Reset()

	// IPC server arranca UNA vez y sobrevive todas las iteraciones
	// del config loop. Sin esto, un re-pair tumbaría el IPC y el tray
	// vería "servicio no responde" durante el reset — UX feo.
	go func() {
		handlers := ipc.Handlers{
			GetStatus: getStatusHandler,
			Restart:   RequestRestartCurrent,
			Pair: func(code, backendURL string) (string, error) {
				resp, err := PairAndApply(code, backendURL)
				if err != nil {
					return "", err
				}
				return resp.AgentID, nil
			},
		}
		if err := ipc.Serve(ctx, handlers); err != nil {
			log.Printf("⚠ IPC server no pudo arrancar: %v", err)
		}
	}()

	stop := ctx.Done()
	for {
		select {
		case <-stop:
			log.Println("✓ Runner terminado (ctx cancelado).")
			return nil
		default:
		}

		// Limpiar señales pendientes antes de leer config — evita
		// reset spurious si el usuario hizo pair y luego ctx cancel.
		drainConfigChanged()

		cfg, err := config.Load()
		if err != nil {
			if errors.Is(err, config.ErrNotConfigured) {
				log.Println("→ sin config: esperando emparejamiento desde el tray o CLI…")
				SetConnected(false)
				SetIdentity("", "")
				SetPrinter("")
				if !waitForConfigChanged(stop) {
					log.Println("✓ Runner terminado (ctx cancelado durante espera).")
					return nil
				}
				log.Println("→ config detectado, recargando…")
				continue
			}
			return err
		}

		// Tenemos config — corre el inner loop que abre SSE.
		// El inner loop sale cuando:
		//   - ctx cancelado (servicio detiéndose) → retornamos.
		//   - configChangedCh señalado (re-pair) → continue para reload.
		if err := runWithConfig(ctx, cfg); err != nil {
			log.Printf("⚠ ciclo SSE termino con error: %v", err)
		}

		if ctx.Err() != nil {
			log.Println("✓ Runner terminado.")
			return nil
		}
		// Si llegamos acá sin ctx.Err es porque configChanged disparó
		// el reset — loop iteración para releer config.
	}
}

// runWithConfig ejecuta una iteración con un config válido: detecta
// impresora, abre SSE, procesa jobs hasta ctx cancel o configChanged.
// Retornar error fatal solo en errores pre-conexión (no impresora);
// errores de SSE se manejan internamente con backoff.
func runWithConfig(ctx context.Context, cfg *config.Config) error {
	SetIdentity(cfg.AgentID, cfg.BackendURL)

	printers, err := printer.ListSystem()
	if err != nil {
		return fmt.Errorf("listando impresoras: %w", err)
	}
	if len(printers) == 0 {
		log.Println("⚠ no hay impresoras instaladas; reintentando en 30s")
		// No abortar — el usuario puede conectar la impresora después.
		// Esperamos un poco y dejamos al outer loop reintentar.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(30 * time.Second):
			return nil
		}
	}

	log.Println("Impresoras detectadas:")
	for i, p := range printers {
		log.Printf("  %d. %s", i+1, p.Name)
	}

	selected := pickPrinter(printers, cfg.PrinterName)
	if selected == nil {
		return fmt.Errorf("no pude seleccionar una impresora")
	}
	log.Printf("→ Usando: %s", selected.Name)
	SetPrinter(selected.Name)

	backend := strings.TrimRight(cfg.BackendURL, "/")
	log.Printf("→ Conectando a %s/printing/agents/stream", backend)

	client := sse.New(backend, cfg.AgentToken)
	client.OnPrintJob = func(job *sse.PrintJob) {
		handleJob(ctx, client, selected, job)
	}
	client.OnConnected = func() {
		SetConnected(true)
	}
	client.OnDisconnected = func(_ error) {
		SetConnected(false)
	}

	// Publicar el cliente activo para que el IPC RestartConnection
	// pueda llamar RequestRestart() en él.
	setCurrentClient(client)
	defer setCurrentClient(nil)

	// Child ctx que se cancela ya sea por ctx outer (shutdown del
	// servicio) o por configChanged (re-pair en caliente).
	innerCtx, innerCancel := context.WithCancel(ctx)
	defer innerCancel()

	// Watcher: si configChanged, cancelar innerCtx para que el SSE
	// salga y el outer loop relea el config.
	go func() {
		select {
		case <-innerCtx.Done():
			return
		case <-configChangedCh:
			log.Println("→ config cambió, cerrando SSE actual para recargar")
			innerCancel()
		}
	}()

	client.RunWithReconnect(innerCtx)
	return nil
}

// getStatusHandler construye el snapshot que el IPC GetStatus retorna.
// Vive separado de Run() porque el IPC server arranca antes del primer
// runWithConfig — necesita ser callable aún sin client activo.
func getStatusHandler() ipc.StatusResponse {
	connected, agentID, backendURL, printerName, lastJobAt := Snapshot()
	lastJobStr := ""
	if !lastJobAt.IsZero() {
		lastJobStr = lastJobAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	return ipc.StatusResponse{
		Connected:   connected,
		AgentID:     agentID,
		BackendURL:  backendURL,
		Version:     AgentVersion,
		LastJobAt:   lastJobStr,
		PrinterName: printerName,
	}
}

// pickPrinter respeta el override de cfg.PrinterName si coincide con
// una impresora instalada. Si no, cae al heurístico SelectThermalPrinter.
func pickPrinter(printers []printer.Printer, preferred string) *printer.Printer {
	if preferred != "" {
		for i := range printers {
			if printers[i].Name == preferred {
				return &printers[i]
			}
		}
		log.Printf("⚠ printer_name=%q de config no coincide con ninguna impresora; usando heurística", preferred)
	}
	return printer.SelectThermalPrinter(printers)
}

// handleJob convierte el payload del job a ESC/POS y lo imprime.
// Reporta resultado al backend sin abortar el runner — si la impresora
// está offline, el agent sigue escuchando jobs futuros.
func handleJob(ctx context.Context, client *sse.Client, p *printer.Printer, job *sse.PrintJob) {
	log.Printf("📄 Job recibido: %s (%s)", job.ID, job.JobType)

	var payload printer.Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		msg := fmt.Sprintf("payload inválido: %v", err)
		log.Printf("✗ %s", msg)
		_ = client.ReportJobResult(ctx, job.ID, "failed", msg)
		return
	}

	escposBytes, err := printer.Convert(payload)
	if err != nil {
		msg := fmt.Sprintf("conversión ESC/POS: %v", err)
		log.Printf("✗ %s", msg)
		_ = client.ReportJobResult(ctx, job.ID, "failed", msg)
		return
	}

	if err := printer.SendRaw(p, escposBytes); err != nil {
		msg := fmt.Sprintf("impresión: %v", err)
		log.Printf("✗ %s", msg)
		_ = client.ReportJobResult(ctx, job.ID, "failed", msg)
		RecordJob()
		return
	}

	log.Printf("✓ Impreso: %s", job.ID)
	_ = client.ReportJobResult(ctx, job.ID, "printed", "")
	RecordJob()
}
