// NovaSoft Print Agent — binary principal.
//
// Subcomandos:
//   pair     Empareja con un restaurante usando un código del wizard.
//   run      Abre SSE con el backend y procesa jobs.
//   status   Muestra la config actual y las impresoras detectadas.
//   unpair   Borra la config local.
//   version  Imprime la versión.
//
// Uso típico:
//   novasoft-agent.exe pair        # primera vez
//   novasoft-agent.exe run         # deja corriendo
//
// Transporte: desde v0.2.0 el agent usa SSE (HTTP/1.1 streaming) en
// lugar de WebSocket. Motivo: Railway+Fastly strippean el header
// Upgrade: websocket antes de llegar al origin, así que la conexión WS
// nunca completaba. SSE funciona sin config especial por ser HTTP puro.
// Ver ADR-015 del backend para detalles.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/pairing"
	"github.com/Richi2489/novasoft-print-agent/internal/printer"
	"github.com/Richi2489/novasoft-print-agent/internal/sse"
)

// Version se inyecta en build vía -ldflags "-X main.Version=vX.Y.Z".
// El default tiene "-dev" para distinguir un build manual sin ldflags.
var Version = "0.1.0-dev"

// DefaultBackendURL se puede sobreescribir con -backend.
// Apunta al Railway prod — el deploy de producción corre ahí. Si Ricardo
// deploya a otro proyecto Railway, override con la flag.
const DefaultBackendURL = "https://novasoft-backend-production.up.railway.app"

func main() {
	// Flags globales. Se parsean antes del subcomando para que funcione
	// "novasoft-agent.exe -backend=X pair".
	backendFlag := flag.String("backend", DefaultBackendURL, "URL del backend (ej: https://api.novasoft.mx)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	cmd := args[0]
	switch cmd {
	case "pair":
		if err := cmdPair(*backendFlag); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
	case "run":
		if err := cmdRun(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
	case "status":
		if err := cmdStatus(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
	case "unpair":
		if err := cmdUnpair(); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(Version)
	default:
		fmt.Fprintf(os.Stderr, "comando desconocido: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `NovaSoft Print Agent `+Version+`

Uso:
  novasoft-agent.exe [-backend=URL] <comando>

Comandos:
  pair     Empareja con un restaurante usando un código del wizard.
  run      Abre WebSocket con el backend y procesa jobs.
  status   Muestra la config actual y las impresoras detectadas.
  unpair   Borra la config local.
  version  Imprime la versión.

Ejemplos:
  novasoft-agent.exe pair
  novasoft-agent.exe run
  novasoft-agent.exe -backend=https://api.novasoft.mx run
`)
}

// cmdPair pide el código por stdin, llama al backend, guarda config.
func cmdPair(backendURL string) error {
	fmt.Println("=== NovaSoft Print Agent — Emparejamiento ===")
	fmt.Print("Ingresa el código que aparece en NovaSoft: ")

	reader := bufio.NewReader(os.Stdin)
	raw, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("leyendo código: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("no ingresaste ningún código")
	}

	fmt.Println("→ Emparejando…")
	resp, err := pairing.Pair(backendURL, raw, Version)
	if err != nil {
		return err
	}

	cfg := &config.Config{
		AgentID:      resp.AgentID,
		AgentToken:   resp.AgentToken,
		WebsocketURL: resp.WebsocketURL,
		BackendURL:   backendURL,
	}
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("guardando config: %w", err)
	}

	path, _ := config.Path()
	fmt.Println()
	fmt.Println("✓ Emparejado correctamente")
	fmt.Printf("  Agent ID: %s\n", resp.AgentID)
	fmt.Printf("  Config:   %s\n", path)
	fmt.Println()
	fmt.Println("Ahora ejecuta:")
	fmt.Println("  novasoft-agent.exe run")
	return nil
}

// cmdRun carga config, detecta impresora, abre SSE, procesa jobs.
func cmdRun() error {
	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			return fmt.Errorf("no hay configuración — ejecuta 'novasoft-agent.exe pair' primero")
		}
		return err
	}

	// 1. Detectar impresora.
	printers, err := printer.ListSystem()
	if err != nil {
		return fmt.Errorf("listando impresoras: %w", err)
	}
	if len(printers) == 0 {
		return fmt.Errorf("no hay impresoras instaladas en Windows — instala el driver de tu impresora primero")
	}

	fmt.Println("Impresoras detectadas:")
	for i, p := range printers {
		fmt.Printf("  %d. %s\n", i+1, p.Name)
	}

	selected := printer.SelectThermalPrinter(printers)
	if selected == nil {
		return fmt.Errorf("no pude seleccionar una impresora")
	}
	fmt.Printf("→ Usando: %s\n", selected.Name)
	fmt.Println()

	// 2. El endpoint SSE se deriva del BackendURL del config (que el
	// agent guardó al emparejarse). Ignoramos cfg.WebsocketURL — ese
	// campo se preserva para back-compat pero en el pivot SSE no lo
	// usamos; el backend del pair response devuelve un URL informativo
	// que puede apuntar a un dominio distinto del Railway URL efectivo
	// (p. ej. api.novasoft.mx sin CNAME).
	backend := strings.TrimRight(cfg.BackendURL, "/")
	fmt.Printf("→ Conectando a %s/printing/agents/stream\n", backend)

	// 3. Cliente SSE con callback de impresión.
	client := sse.New(backend, cfg.AgentToken)
	// Context cancelable por signal — lo creamos antes del callback para
	// que handleJob pueda usarlo como parent del POST de reporte.
	ctx, cancel := signalContext()
	defer cancel()

	client.OnPrintJob = func(job *sse.PrintJob) {
		handleJob(ctx, client, selected, job)
	}

	client.RunWithReconnect(ctx)

	fmt.Println()
	fmt.Println("✓ Agent cerrado.")
	return nil
}

// handleJob convierte el payload del job a ESC/POS y lo imprime.
// Reporta resultado al backend sin fallar el agent — si la impresora
// está offline, el agent sigue escuchando por si llega el siguiente.
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
		return
	}

	log.Printf("✓ Impreso: %s", job.ID)
	_ = client.ReportJobResult(ctx, job.ID, "printed", "")
}

// cmdStatus imprime info diagnóstica para troubleshoot.
func cmdStatus() error {
	cfg, err := config.Load()
	path, _ := config.Path()

	fmt.Printf("Version:       %s\n", Version)
	fmt.Printf("Config path:   %s\n", path)
	fmt.Println()

	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			fmt.Println("Config:        (no hay — ejecuta 'pair' primero)")
		} else {
			fmt.Printf("Config:        error — %v\n", err)
		}
	} else {
		fmt.Printf("Agent ID:      %s\n", cfg.AgentID)
		fmt.Printf("Backend URL:   %s\n", cfg.BackendURL)
		fmt.Printf("Stream URL:    %s/printing/agents/stream\n",
			strings.TrimRight(cfg.BackendURL, "/"))
		if cfg.PrinterName != "" {
			fmt.Printf("Printer:       %s (fijada en config)\n", cfg.PrinterName)
		}
	}
	fmt.Println()

	printers, err := printer.ListSystem()
	if err != nil {
		return fmt.Errorf("listando impresoras: %w", err)
	}
	fmt.Printf("Impresoras detectadas (%d):\n", len(printers))
	for i, p := range printers {
		fmt.Printf("  %d. %s\n", i+1, p.Name)
	}
	if selected := printer.SelectThermalPrinter(printers); selected != nil {
		fmt.Printf("Preferida: %s\n", selected.Name)
	}
	return nil
}

// cmdUnpair borra el config tras confirmación.
func cmdUnpair() error {
	fmt.Print("¿Seguro que quieres borrar la configuración? (y/n): ")
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))
	if ans != "y" && ans != "yes" {
		fmt.Println("Cancelado.")
		return nil
	}
	if err := config.Delete(); err != nil {
		return err
	}
	fmt.Println("✓ Config borrada.")
	return nil
}

// signalContext construye un context que se cancela al recibir
// SIGINT (Ctrl-C) o SIGTERM. Permite graceful shutdown del run loop.
func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println()
		log.Println("→ señal recibida, cerrando…")
		cancel()
	}()
	return ctx, cancel
}
