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

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/ipc"
	"github.com/Richi2489/novasoft-print-agent/internal/printer"
	"github.com/Richi2489/novasoft-print-agent/internal/sse"
)

// AgentVersion es el string de versión del binary que se reporta vía
// IPC al tray. main() lo asigna al inicio (es la misma var que se
// inyecta con -ldflags).
var AgentVersion = "0.3.0-dev"

// Run carga config, detecta impresora, abre SSE, expone IPC, y procesa
// jobs hasta que ctx se cancela. Errores pre-loop (no hay config, no
// hay impresora) se devuelven inmediatamente; errores post-conexión se
// loggean y el SSE reintenta con backoff exponencial.
//
// Side-effects:
//   - Publica estado en runner.Snapshot() (consultado por el IPC server).
//   - Levanta el IPC server en \\.\pipe\NovaSoftAgent (best-effort: si
//     falla se loggea pero el runner sigue — la impresión es prioritaria
//     sobre la diagnóstica).
//
// Los errores devueltos preservan errors.Is(config.ErrNotConfigured)
// para que el caller distinga "no emparejado" de otros fallos.
func Run(ctx context.Context) error {
	// Reset al arranque para no arrastrar estado de runs anteriores
	// dentro del mismo proceso (caso del servicio: Stop+Start sin morir).
	Reset()

	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			return fmt.Errorf("no hay configuración — ejecuta 'novasoft-agent.exe pair' primero")
		}
		return err
	}
	SetIdentity(cfg.AgentID, cfg.BackendURL)

	printers, err := printer.ListSystem()
	if err != nil {
		return fmt.Errorf("listando impresoras: %w", err)
	}
	if len(printers) == 0 {
		return fmt.Errorf("no hay impresoras instaladas en Windows — instala el driver primero")
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

	// Levantar IPC en background. Si falla (pipe ya en uso, permission
	// denied), loguear y seguir — la impresión funciona sin IPC, solo
	// el tray no podrá mostrar estado.
	go func() {
		handlers := buildIPCHandlers(client)
		if err := ipc.Serve(ctx, handlers); err != nil {
			log.Printf("⚠ IPC server no pudo arrancar: %v", err)
		}
	}()

	client.RunWithReconnect(ctx)

	log.Println("✓ Runner terminado.")
	return nil
}

// buildIPCHandlers construye los callbacks que el IPC server invoca
// por cada comando. El runner los expone vía closures sobre el sse.Client
// y el state package-level.
func buildIPCHandlers(client *sse.Client) ipc.Handlers {
	return ipc.Handlers{
		GetStatus: func() ipc.StatusResponse {
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
		},
		Restart: func() error {
			client.RequestRestart()
			return nil
		},
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
