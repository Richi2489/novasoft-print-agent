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
	"github.com/Richi2489/novasoft-print-agent/internal/logging"
	"github.com/Richi2489/novasoft-print-agent/internal/pairing"
	"github.com/Richi2489/novasoft-print-agent/internal/printer"
	"github.com/Richi2489/novasoft-print-agent/internal/sse"
	"github.com/Richi2489/novasoft-print-agent/internal/winsvc"
)

// Version se inyecta en build vía -ldflags "-X main.Version=vX.Y.Z".
// El default tiene "-dev" para distinguir un build manual sin ldflags.
var Version = "v0.4.0-dev"

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

func usage() {
	fmt.Fprintln(os.Stderr, `NovaSoft Print Agent `+Version+`

Uso:
  novasoft-agent.exe [-backend=URL] <comando>

Comandos:
  pair       Empareja con un restaurante usando un código del wizard
             (pair ABCD-1234-WXYZ, o sin código para teclearlo).
  run       Arranca el loop principal en esta consola — escucha jobs.
  service    Administra el servicio de Windows (ver abajo).
  status     Muestra la config actual y las impresoras detectadas.
  unpair     Borra la config local.
  version    Imprime la versión.

Subcomandos de service (install/uninstall/start/stop requieren
PowerShell "Ejecutar como administrador"):
  service install     Instala el servicio con arranque automático y
                      reinicio ante fallo. Es la forma recomendada.
  service uninstall   Detiene y elimina el servicio.
  service start       Arranca el servicio.
  service stop        Detiene el servicio.
  service status      Estado, cuenta, recuperación y ruta del log.

Ejemplos:
  novasoft-agent.exe pair
  novasoft-agent.exe service install
  novasoft-agent.exe service status
  novasoft-agent.exe -backend=https://api.novasoft.mx run
`)
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
	fmt.Println("Ahora instala el servicio para que arranque solo:")
	fmt.Println("  novasoft-agent.exe service install")
	fmt.Println("  novasoft-agent.exe service start")
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

// cmdRun decide entre modo servicio y modo consola.
//
// El SCM arranca el binario con el mismo subcomando `run`, así que la
// única diferencia la marca svc.IsWindowsService(). Mantener un solo
// subcomando evita el clásico bug de instalar el servicio apuntando a un
// modo y probar a mano el otro.
func cmdRun() error {
	if winsvc.IsService() {
		return runAsService()
	}
	return runInteractive()
}

// runAsService entrega el control al SCM. Sin consola: todo va al log de
// archivo y los hitos al Event Log de Windows.
func runAsService() error {
	// El log a archivo es obligatorio aquí: sin él el servicio sería una
	// caja negra. Si no se puede abrir, mejor fallar y que el SCM lo
	// reporte que correr a ciegas.
	logPath, closer, err := logging.Setup(false)
	if err != nil {
		return fmt.Errorf("no pude abrir el log en %s: %w", logPath, err)
	}
	if closer != nil {
		defer closer.Close()
	}

	log.Printf("=== NovaSoft Print Agent %s — modo servicio ===", Version)
	return winsvc.Run(func(ctx context.Context) error {
		return runAgent(ctx)
	})
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
	printers, err := printer.ListSystem()
	if err != nil {
		return fmt.Errorf("listando impresoras: %w", err)
	}
	if len(printers) == 0 {
		return fmt.Errorf("no hay impresoras instaladas en Windows — instala el driver de tu impresora primero")
	}

	log.Println("Impresoras detectadas:")
	for i, p := range printers {
		log.Printf("  %d. %s", i+1, p.Name)
	}

	selected := printer.SelectThermalPrinter(printers)
	if selected == nil {
		return fmt.Errorf("no pude seleccionar una impresora")
	}
	log.Printf("→ Usando: %s", selected.Name)

	// 2. El endpoint SSE se deriva del BackendURL del config (que el
	// agent guardó al emparejarse). Ignoramos cfg.WebsocketURL — ese
	// campo se preserva para back-compat pero en el pivot SSE no lo
	// usamos; el backend del pair response devuelve un URL informativo
	// que puede apuntar a un dominio distinto del Railway URL efectivo
	// (p. ej. api.novasoft.mx sin CNAME).
	backend := strings.TrimRight(cfg.BackendURL, "/")
	log.Printf("→ Conectando a %s/printing/agents/stream", backend)

	// 3. Cliente SSE con callback de impresión.
	client := sse.New(backend, cfg.AgentToken)
	client.OnPrintJob = func(job *sse.PrintJob) {
		handleJob(ctx, client, selected, job)
	}

	// Bloquea hasta que ctx se cancele (Ctrl-C o Stop del SCM),
	// reconectando con backoff exponencial mientras tanto.
	client.RunWithReconnect(ctx)
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

// cmdService despacha los subcomandos de administración del servicio.
func cmdService(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("falta el subcomando — usa: service install|uninstall|start|stop|status")
	}

	switch args[0] {
	case "install":
		return cmdServiceInstall()

	case "uninstall":
		if err := winsvc.Uninstall(); err != nil {
			return err
		}
		fmt.Printf("✓ Servicio %q desinstalado.\n", winsvc.Name)
		fmt.Println()
		fmt.Println("La configuración y los logs NO se borraron. Si quieres")
		fmt.Println("dejar el equipo limpio del todo:")
		dir, _ := config.DataDir()
		fmt.Println("  novasoft-agent.exe unpair    (borra el token)")
		fmt.Printf("  y elimina a mano: %s\n", dir)
		return nil

	case "start":
		if err := winsvc.Start(); err != nil {
			return err
		}
		fmt.Printf("✓ Servicio %q arrancado.\n", winsvc.Name)
		logPath, _ := logging.Path()
		fmt.Printf("  Log: %s\n", logPath)
		return nil

	case "stop":
		if err := winsvc.Stop(); err != nil {
			return err
		}
		fmt.Printf("✓ Servicio %q detenido.\n", winsvc.Name)
		return nil

	case "status":
		return cmdServiceStatus()

	default:
		return fmt.Errorf("subcomando de service desconocido: %s — usa: install|uninstall|start|stop|status", args[0])
	}
}

// cmdServiceInstall instala el servicio, migrando antes el config si
// venía de una versión que lo guardaba en %APPDATA%.
//
// El orden importa: la migración tiene que correr AQUÍ, con el usuario
// que hizo el `pair` (y su %APPDATA%), porque una vez instalado el
// servicio corre como LocalSystem y ya no vería ese directorio.
func cmdServiceInstall() error {
	// 1. Migración de config vieja → %PROGRAMDATA%.
	migrated, err := config.MigrateLegacy()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  no pude migrar la config vieja: %v\n", err)
	} else if migrated {
		newPath, _ := config.Path()
		fmt.Printf("✓ Config migrada desde %s\n", config.LegacyPath())
		fmt.Printf("  a %s (no hace falta volver a emparejar).\n\n", newPath)
	}

	// 2. Aviso temprano si todavía no hay emparejamiento. No abortamos:
	// instalar primero y emparejar después es un orden válido.
	pairedOK := true
	if _, err := config.Load(); err != nil {
		pairedOK = false
	}

	// 3. Crear el servicio.
	if err := winsvc.Install(); err != nil {
		return err
	}

	exe, _ := winsvc.ExePath()
	logPath, _ := logging.Path()
	cfgPath, _ := config.Path()

	fmt.Printf("✓ Servicio %q instalado.\n\n", winsvc.Name)
	fmt.Printf("  Nombre:      %s (%s)\n", winsvc.Name, winsvc.DisplayName)
	fmt.Printf("  Ejecutable:  %s run\n", exe)
	fmt.Println("  Cuenta:      LocalSystem")
	fmt.Println("  Arranque:    Automático (con el equipo, sin necesidad de iniciar sesión)")
	fmt.Println("  Recuperación: reinicio a los 5 s, 5 s y luego cada 30 s")
	fmt.Printf("  Config:      %s\n", cfgPath)
	fmt.Printf("  Log:         %s\n", logPath)
	fmt.Println()

	if !pairedOK {
		fmt.Println("⚠️  Todavía no hay emparejamiento. Antes de arrancar el servicio:")
		fmt.Println("      novasoft-agent.exe pair")
		fmt.Println()
	}

	fmt.Println("Para arrancarlo ahora:")
	fmt.Println("  novasoft-agent.exe service start")
	return nil
}

// cmdServiceStatus imprime el estado del servicio para soporte.
// No requiere permisos de administrador — es lo primero que se pide por
// teléfono cuando un restaurante reporta que dejó de imprimir.
func cmdServiceStatus() error {
	info, err := winsvc.Query()
	if err != nil {
		return err
	}

	logPath, _ := logging.Path()
	cfgPath, _ := config.Path()

	fmt.Printf("Version:       %s\n", Version)
	fmt.Printf("Servicio:      %s\n", winsvc.Name)
	fmt.Println()

	if !info.Installed {
		fmt.Println("Estado:        NO INSTALADO")
		fmt.Println()
		fmt.Println("Para instalarlo, en PowerShell como administrador:")
		fmt.Println("  novasoft-agent.exe service install")
		fmt.Println("  novasoft-agent.exe service start")
	} else {
		fmt.Printf("Estado:        %s\n", winsvc.StateName(info.State))
		if info.ProcessID != 0 {
			fmt.Printf("PID:           %d\n", info.ProcessID)
		}
		fmt.Printf("Arranque:      %s\n", winsvc.StartTypeName(info.StartType))
		fmt.Printf("Cuenta:        %s\n", info.Account)
		fmt.Printf("Ejecutable:    %s\n", info.BinaryPath)
		if len(info.Recovery) == 0 {
			fmt.Println("Recuperación:  (ninguna configurada) ⚠️")
		} else {
			fmt.Println("Recuperación:")
			for i, a := range info.Recovery {
				fmt.Printf("  fallo %d:     %s\n", i+1, winsvc.RecoveryActionName(a))
			}
			fmt.Println("  siguientes:  como el último")
		}
	}

	fmt.Println()
	fmt.Printf("Config:        %s\n", cfgPath)
	if _, err := config.Load(); err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			fmt.Println("               (no emparejado — ejecuta 'pair')")
		} else {
			fmt.Printf("               error: %v\n", err)
		}
	} else {
		fmt.Println("               (emparejado ✓)")
	}
	fmt.Printf("Log:           %s\n", logPath)
	if st, err := os.Stat(logPath); err == nil {
		fmt.Printf("               (%d KB, última escritura %s)\n",
			st.Size()/1024, st.ModTime().Format("2006-01-02 15:04:05"))
	} else {
		fmt.Println("               (todavía no existe)")
	}
	fmt.Println()
	fmt.Println("Eventos de arranque/parada/errores fatales: Visor de eventos")
	fmt.Printf("de Windows → Registros de Windows → Aplicación → origen %q.\n", winsvc.Name)
	return nil
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

	if info, qErr := winsvc.Query(); qErr == nil {
		if info.Installed {
			fmt.Printf("Servicio:      instalado, %s\n", winsvc.StateName(info.State))
		} else {
			fmt.Println("Servicio:      no instalado (ver 'service install')")
		}
		fmt.Println()
	}

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
