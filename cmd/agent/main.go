// NovaSoft Print Agent — binary principal.
//
// Subcomandos:
//
//	pair      Empareja con un restaurante usando un código del wizard.
//	run       Abre SSE con el backend y procesa jobs.
//	service   Instala/administra el servicio de Windows.
//	status    Muestra la config actual y las impresoras detectadas.
//	unpair    Borra la config local.
//	version   Imprime la versión.
//
// Uso típico (recomendado, desde PowerShell como administrador):
//
//	novasoft-agent.exe pair                # primera vez
//	novasoft-agent.exe service install     # queda como servicio
//	novasoft-agent.exe service start
//
// Uso manual (diagnóstico):
//
//	novasoft-agent.exe run                 # deja corriendo en consola
//
// Modo servicio: `run` es también el subcomando con el que el Service
// Control Manager arranca el binario. El propio proceso detecta si viene
// del SCM (svc.IsWindowsService) y elige modo servicio —sin consola,
// log a archivo, Stop/Shutdown manejados— o modo interactivo de siempre.
//
// Transporte: desde v0.2.0 el agent usa SSE (HTTP/1.1 streaming) en
// lugar de WebSocket. Motivo: Railway+Fastly strippean el header
// Upgrade: websocket antes de llegar al origin, así que la conexión WS
// nunca completaba. SSE funciona sin config especial por ser HTTP puro.
// Ver ADR-015 del backend para detalles.
//
// Protocolo v2 (desde v0.5.0): diario local de trabajos
// (%PROGRAMDATA%\NovaSoftAgent\journal.json) + saludo con los resultados
// recientes antes de cada conexión. Con eso el backend reentrega lo que
// quedó sin confirmar en vez de marcarlo "failed", y este agente NUNCA
// imprime dos veces la misma entrega. Contrato: docs/printing/protocolo-v2.md
// del backend.
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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/journal"
	"github.com/Richi2489/novasoft-print-agent/internal/logging"
	"github.com/Richi2489/novasoft-print-agent/internal/pairing"
	"github.com/Richi2489/novasoft-print-agent/internal/printer"
	"github.com/Richi2489/novasoft-print-agent/internal/sse"
)

// Version se inyecta en build vía -ldflags "-X main.Version=vX.Y.Z".
// El default tiene "-dev" para distinguir un build manual sin ldflags.
var Version = "v0.5.0-dev"

// versionReportada es la versión que se le informa al backend (pair,
// saludo, cabecera del stream): sin la "v" inicial, p. ej. "0.5.0".
func versionReportada() string {
	return strings.TrimPrefix(Version, "v")
}

// Diario y reportes (protocolo v2).
const (
	// maxResultadosSaludo — tope que acepta el backend en el saludo.
	maxResultadosSaludo = 200
	// cadaReintentoDeReportes — cada cuánto se reintentan los resultados
	// que no se pudieron reportar (POST /result que falló por red).
	cadaReintentoDeReportes = 60 * time.Second
)

// DefaultBackendURL se puede sobreescribir con -backend.
// Dominio público del backend. El backend sólo acepta hosts *.novasoft.mx
// (TrustedHostMiddleware, desde abr-2026): la URL *.up.railway.app responde
// 400 "Invalid host header". v0.3.0 ya apuntaba aquí; v0.4.0 regresó a la
// URL de Railway por error y por eso el instalador v0.4.1 no emparejaba
// (prueba del 2026-10-07). Se puede sobreescribir con -backend.
const DefaultBackendURL = "https://api.novasoft.mx"

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
		if err := cmdPair(*backendFlag, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "✗ %s\n", err)
			os.Exit(pairExitCode(err))
		}
	case "run":
		fail(cmdRun())
	case "service":
		fail(cmdService(args[1:]))
	case "status":
		fail(cmdStatus())
	case "unpair":
		fail(cmdUnpair())
	case "printer":
		fail(cmdPrinter(args[1:]))
	case "test-print":
		fail(cmdTestPrint(args[1:]))
	case "version":
		fmt.Println(Version)
	default:
		fmt.Fprintf(os.Stderr, "comando desconocido: %s\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

// fail imprime el error y termina con exit 1. Centraliza el patrón que
// antes se repetía en cada case del switch.
func fail(err error) {
	if err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "✗ %s\n", err)
	os.Exit(1)
}

// cmdPair empareja con el código del wizard, llama al backend y guarda
// config. El código llega como argumento (`pair ABCD-1234-WXYZ`, lo usa el
// instalador para validarlo sin consola) o, si no viene, se pide por stdin.
func cmdPair(backendURL string, args []string) error {
	fmt.Println("=== NovaSoft Print Agent — Emparejamiento ===")

	var raw string
	if len(args) > 0 {
		raw = args[0]
	} else {
		fmt.Print("Ingresa el código que aparece en NovaSoft: ")
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("leyendo código: %w", err)
		}
		raw = line
	}
	raw = strings.TrimSpace(raw) // pairing.NormalizeCode hace el resto
	if raw == "" {
		return fmt.Errorf("no ingresaste ningún código")
	}

	fmt.Println("→ Emparejando…")
	resp, err := pairing.Pair(backendURL, raw, versionReportada())
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
		// El caso frecuente: %PROGRAMDATA% no es escribible sin elevar.
		return fmt.Errorf(`guardando config: %w

Si el error menciona acceso denegado, abre PowerShell como
administrador (click derecho → "Ejecutar como administrador") y repite
el emparejamiento`, err)
	}

	path, _ := config.Path()
	fmt.Println()
	fmt.Println("✓ Emparejado correctamente")
	fmt.Printf("  Agent ID: %s\n", resp.AgentID)
	fmt.Printf("  Config:   %s\n", path)
	fmt.Println()
	printPostPairHint()
	return nil
}

// Códigos de salida de `pair`. Son contrato con el instalador
// (build/installer/novasoft-agent-setup.iss): con ellos muestra el mensaje
// exacto sin leer la consola. No renumerar sin cambiar el instalador.
const (
	exitPairOtro     = 1 // cualquier otra falla (config, servidor 5xx…)
	exitPairInvalido = 2
	exitPairUsado    = 3
	exitPairVencido  = 4
	exitPairSinRed   = 5
)

func pairExitCode(err error) int {
	switch {
	case errors.Is(err, pairing.ErrCodeInvalid):
		return exitPairInvalido
	case errors.Is(err, pairing.ErrCodeUsed):
		return exitPairUsado
	case errors.Is(err, pairing.ErrCodeExpired):
		return exitPairVencido
	case errors.Is(err, pairing.ErrNoNetwork):
		return exitPairSinRed
	default:
		return exitPairOtro
	}
}

// runInteractive es el comportamiento de toda la vida: salida por
// consola, Ctrl-C para salir. Además escribe al mismo log de archivo,
// para que un diagnóstico a mano deje rastro.
func runInteractive() error {
	logPath, closer, err := logging.Setup(true)
	if err != nil {
		// A mano esto NO es fatal: un usuario sin permisos de escritura
		// en %PROGRAMDATA% igual debe poder correr el agent y ver la
		// salida en pantalla.
		fmt.Fprintf(os.Stderr, "⚠️  no pude escribir el log en %s (%v) — sigo solo en consola\n", logPath, err)
	} else if closer != nil {
		defer closer.Close()
	}

	fmt.Printf("=== NovaSoft Print Agent %s ===\n", Version)
	fmt.Printf("Log: %s\n\n", logPath)

	ctx, cancel := signalContext()
	defer cancel()

	if err := runAgent(ctx); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("✓ Agent cerrado.")
	return nil
}

// runAgent es el loop principal, común a los dos modos: carga config,
// detecta impresora, abre SSE y procesa jobs hasta que ctx se cancele.
//
// Usa log.Printf (no fmt.Println) a propósito: así la misma salida sirve
// para la consola en modo manual y para el archivo en modo servicio.
func runAgent(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			path, _ := config.Path()
			return fmt.Errorf("no hay configuración en %s — ejecuta 'novasoft-agent.exe pair' primero", path)
		}
		return err
	}

	// 1. Detectar impresora.
	selected, err := elegirImpresora(cfg)
	if err != nil {
		return err
	}

	// 2. El endpoint SSE se deriva del BackendURL del config (que el
	// agent guardó al emparejarse). Ignoramos cfg.WebsocketURL — ese
	// campo se preserva para back-compat pero en el pivot SSE no lo
	// usamos; el backend del pair response devuelve un URL informativo
	// que puede apuntar a un dominio distinto del Railway URL efectivo
	// (p. ej. api.novasoft.mx sin CNAME).
	backend := strings.TrimRight(cfg.BackendURL, "/")

	// 3. Diario local (protocolo v2). Si no se puede abrir, el agente
	// sigue imprimiendo: perder la garantía anti-duplicado tras un
	// reinicio es malo, pero dejar al restaurante sin tickets es peor.
	diario, err := abrirDiario()
	if err != nil {
		log.Printf("⚠️  no pude abrir el diario de trabajos (%v) — se imprime sin protección anti-duplicado", err)
	} else {
		log.Printf("→ Diario de trabajos: %s (%d entradas, se guardan 7 días)", diario.Ruta(), diario.Total())
	}

	log.Printf("→ Conectando a %s/printing/agents/stream (protocolo v%d, agente %s)",
		backend, sse.ProtocolVersion, versionReportada())

	// 4. Cliente SSE con saludo + callback de impresión.
	client := sse.New(backend, cfg.AgentToken)
	client.AgentVersion = versionReportada()
	client.OnPrintJob = func(job *sse.PrintJob) {
		handleJob(ctx, client, diario, selected, job)
	}
	if diario != nil {
		client.BeforeConnect = func(ctx context.Context) error {
			return saludar(ctx, client, diario)
		}
		go reintentarReportes(ctx, client, diario)
	}

	// Bloquea hasta que ctx se cancele (Ctrl-C o Stop del SCM),
	// reconectando con backoff exponencial con jitter mientras tanto.
	client.RunWithReconnect(ctx)
	return nil
}

// abrirDiario abre %PROGRAMDATA%\NovaSoftAgent\journal.json.
func abrirDiario() (*journal.Diario, error) {
	dir, err := config.EnsureDataDir()
	if err != nil {
		return nil, err
	}
	return journal.Abrir(filepath.Join(dir, "journal.json"))
}

// saludar manda el saludo v2 con los resultados recientes del diario. Lo
// que el backend recibió ahí queda como reportado.
func saludar(ctx context.Context, client *sse.Client, diario *journal.Diario) error {
	_ = diario.Podar()
	recientes := diario.Recientes(maxResultadosSaludo)
	results := make([]sse.RecentResult, 0, len(recientes))
	claves := make([]string, 0, len(recientes))
	for _, e := range recientes {
		r := sse.RecentResult{
			JobID:        e.JobID,
			Status:       e.Estado,
			ErrorMessage: e.Mensaje,
		}
		if e.DeliveryKey != e.JobID {
			r.DeliveryKey = e.DeliveryKey
		}
		if !e.Terminado.IsZero() {
			r.FinishedAt = e.Terminado.UTC().Format(time.RFC3339)
		}
		results = append(results, r)
		claves = append(claves, e.DeliveryKey)
	}
	resp, err := client.Hello(ctx, results)
	if err != nil {
		return err
	}
	_ = diario.MarcarReportados(claves...)
	if resp.AppliedResults > 0 {
		log.Printf("✓ Saludo: el servidor cerró %d ticket(s) con el resultado del diario", resp.AppliedResults)
	}
	return nil
}

// reintentarReportes reenvía en segundo plano los resultados cuyo POST
// /result falló (corte de red justo después de imprimir).
func reintentarReportes(ctx context.Context, client *sse.Client, diario *journal.Diario) {
	t := time.NewTicker(cadaReintentoDeReportes)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for _, e := range diario.SinReportar() {
			if ctx.Err() != nil {
				return
			}
			if _, err := client.ReportJobResult(ctx, e.JobID, e.Estado, e.Mensaje, e.DeliveryKey); err != nil {
				var eh *sse.ErrHTTP
				if errors.As(err, &eh) && eh.Status == 404 {
					// El job ya no existe para este agente (borrado o de otro
					// emparejamiento): no tiene caso insistir.
					_ = diario.MarcarReportados(e.DeliveryKey)
					continue
				}
				break // sin red: se intenta en la siguiente vuelta
			}
			_ = diario.MarcarReportados(e.DeliveryKey)
			log.Printf("✓ Resultado pendiente reportado: %s (%s)", e.JobID, e.Estado)
		}
	}
}

// handleJob convierte el payload del job a ESC/POS y lo imprime, pasando
// SIEMPRE por el diario: una misma delivery_key nunca se imprime dos veces.
// Reporta el resultado al backend sin fallar el agent — si la impresora
// está offline, el agent sigue escuchando por si llega el siguiente.
func handleJob(ctx context.Context, client *sse.Client, diario *journal.Diario, p *printer.Printer, job *sse.PrintJob) {
	clave := job.Key()
	if job.Redelivery {
		log.Printf("📄 Job recibido (reentrega %d): %s (%s)", job.DeliveryAttempt, job.ID, job.JobType)
	} else {
		log.Printf("📄 Job recibido: %s (%s)", job.ID, job.JobType)
	}

	if diario != nil {
		decision, previa, err := diario.Comenzar(job.ID, clave)
		if err != nil {
			log.Printf("⚠️  diario: %v", err)
		}
		switch decision {
		case journal.YaResuelto:
			log.Printf("↺ %s ya estaba %s en el diario — NO se reimprime; se reporta el resultado guardado", job.ID, previa.Estado)
			reportar(ctx, client, diario, job.ID, clave, previa.Estado, previa.Mensaje)
			return
		case journal.EnCurso:
			log.Printf("↺ %s se está imprimiendo ahora mismo — se ignora la copia", job.ID)
			return
		case journal.Interrumpido:
			log.Printf("⚠️  %s quedó a medias en un arranque anterior — NO se reimprime: %s", job.ID, previa.Mensaje)
			reportar(ctx, client, diario, job.ID, clave, previa.Estado, previa.Mensaje)
			return
		}
	}

	estado, msg := imprimirFn(p, job)
	if estado == journal.EstadoImpreso {
		log.Printf("✓ Impreso: %s", job.ID)
	} else {
		log.Printf("✗ %s: %s", job.ID, msg)
	}
	if diario != nil {
		if err := diario.Terminar(clave, estado, msg); err != nil {
			log.Printf("⚠️  diario: %v", err)
		}
	}
	reportar(ctx, client, diario, job.ID, clave, estado, msg)
}

// imprimirFn es imprimir; variable para que los tests cuenten impresiones
// sin una impresora real.
var imprimirFn = imprimir

// imprimir hace el trabajo físico y devuelve ("printed"|"failed", mensaje).
func imprimir(p *printer.Printer, job *sse.PrintJob) (string, string) {
	var payload printer.Payload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return journal.EstadoFallido, fmt.Sprintf("payload inválido: %v", err)
	}
	escposBytes, err := printer.Convert(payload)
	if err != nil {
		return journal.EstadoFallido, fmt.Sprintf("conversión ESC/POS: %v", err)
	}
	if err := printer.SendRaw(p, escposBytes); err != nil {
		return journal.EstadoFallido, fmt.Sprintf("impresión: %v", err)
	}
	return journal.EstadoImpreso, ""
}

// reportar manda el resultado al backend; si no se puede, queda pendiente
// en el diario y lo reintenta reintentarReportes (y el siguiente saludo).
func reportar(ctx context.Context, client *sse.Client, diario *journal.Diario, jobID, clave, estado, msg string) {
	applied, err := client.ReportJobResult(ctx, jobID, estado, msg, clave)
	if err != nil {
		log.Printf("⚠️  no pude reportar %s (%v) — se reintenta solo", jobID, err)
		return
	}
	if !applied {
		log.Printf("ℹ️  el servidor ignoró el resultado de %s (había un reintento más nuevo)", jobID)
	}
	if diario != nil {
		_ = diario.MarcarReportados(clave)
	}
}

// cmdStatus imprime info diagnóstica para troubleshoot.
func cmdStatus() error {
	cfg, err := config.Load()
	path, _ := config.Path()
	logPath, _ := logging.Path()

	fmt.Printf("Version:       %s\n", Version)
	fmt.Printf("Config path:   %s\n", path)
	fmt.Printf("Log path:      %s\n", logPath)
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

	printServiceLine()

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
// Solo se usa en modo interactivo — como servicio el shutdown lo pide
// el SCM, no una señal.
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
