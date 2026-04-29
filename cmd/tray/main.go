// NovaSoft Print Agent — tray icon.
//
// Binary separado del agent core. Corre en sesión usuario (no servicio)
// porque las APIs de Windows tray (Shell_NotifyIcon) requieren acceso
// al desktop interactivo, que el servicio LocalSystem no tiene.
//
// Comunicación con el servicio: named pipe \\.\pipe\NovaSoftAgent.
// Polling cada 3 segundos para mantener el icono fresco.
//
// Menú:
//   - Estado: conectado / desconectado / servicio no responde (label).
//   - Última actividad: hace X min (label).
//   - Reiniciar conexión: IPC RESTART_CONNECTION.
//   - Abrir logs: notepad %PROGRAMDATA%\NovaSoft\agent.log.
//   - Acerca de: version, agent_id, backend.
//   - Salir: cierra el tray (el servicio sigue corriendo).
package main

import (
	_ "embed"
	"fmt"
	"log"
	"os/exec"
	"time"

	"fyne.io/systray"

	"github.com/Richi2489/novasoft-print-agent/internal/ipc"
	"github.com/Richi2489/novasoft-print-agent/internal/logfile"
)

// Version se inyecta en build vía -ldflags "-X main.Version=vX.Y.Z".
var Version = "0.3.0-dev"

// Iconos embebidos. Generados por tools/icongen y commiteados al repo.
// embed.FS sería overkill — son 2 archivos pequeños, []byte directo va bien.
//
//go:embed icons/online.ico
var onlineIcon []byte

//go:embed icons/offline.ico
var offlineIcon []byte

// Polling interval — cuánto esperamos entre status checks contra el
// servicio. 3s es responsive sin saturar el pipe.
const pollInterval = 3 * time.Second

// Items del menú declarados como package vars para que el goroutine de
// poll los pueda actualizar y el de clicks los pueda escuchar.
var (
	statusItem        *systray.MenuItem
	lastActivityItem  *systray.MenuItem
	restartItem       *systray.MenuItem
	logsItem          *systray.MenuItem
	aboutItem         *systray.MenuItem
	quitItem          *systray.MenuItem

	// lastStatus guarda el último estado conocido para que el handler
	// de "Acerca de" tenga datos sin hacer un llamado IPC extra.
	lastStatus *ipc.StatusResponse
)

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(offlineIcon)
	systray.SetTitle("NovaSoft Agent")
	systray.SetTooltip("NovaSoft Print Agent — sin conexión")

	// Items de estado (no clickeables — solo display).
	statusItem = systray.AddMenuItem("Estado: consultando…", "")
	statusItem.Disable()
	lastActivityItem = systray.AddMenuItem("Última actividad: —", "")
	lastActivityItem.Disable()

	systray.AddSeparator()

	// Items de acción.
	restartItem = systray.AddMenuItem("Reiniciar conexión", "Forzar al agent a reconectarse al servidor")
	logsItem = systray.AddMenuItem("Abrir logs", "Abrir agent.log en el editor por defecto")
	aboutItem = systray.AddMenuItem("Acerca de NovaSoft Agent", "Información de versión y configuración")

	systray.AddSeparator()

	quitItem = systray.AddMenuItem("Salir", "Cerrar este icono (el servicio sigue corriendo)")

	go pollLoop()
	go clickLoop()
}

// onExit se invoca cuando systray.Quit() es llamado o el proceso recibe
// SIGINT. No hay cleanup particular — el polling goroutine muere cuando
// el proceso termina.
func onExit() {
	log.Println("→ tray cerrado")
}

// pollLoop consulta el estado del servicio cada pollInterval y actualiza
// el menú + el icono. Si el pipe no responde (servicio detenido o no
// instalado), el estado se reporta como "servicio no responde" y el
// icono queda en modo offline.
func pollLoop() {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()

	// Primera consulta inmediata para que el usuario no vea "consultando…"
	// por 3 segundos al abrir el tray.
	updateStatus()

	for range tick.C {
		updateStatus()
	}
}

// updateStatus hace un round-trip IPC y actualiza los items del menú
// + el icono. Errores de IPC (servicio no corriendo) se traducen al
// label "servicio no responde".
func updateStatus() {
	st, err := ipc.GetStatus()
	if err != nil {
		statusItem.SetTitle("Estado: servicio no responde")
		lastActivityItem.SetTitle("Última actividad: —")
		systray.SetIcon(offlineIcon)
		systray.SetTooltip("NovaSoft Print Agent — servicio detenido")
		lastStatus = nil
		return
	}

	lastStatus = st
	if st.Connected {
		statusItem.SetTitle("Estado: conectado ✓")
		systray.SetIcon(onlineIcon)
		systray.SetTooltip("NovaSoft Print Agent — conectado")
	} else {
		statusItem.SetTitle("Estado: desconectado")
		systray.SetIcon(offlineIcon)
		systray.SetTooltip("NovaSoft Print Agent — reconectando")
	}

	if st.LastJobAt == "" {
		lastActivityItem.SetTitle("Última actividad: ninguna")
	} else {
		// LastJobAt viene en RFC3339 UTC. Convertir a "hace X min".
		t, err := time.Parse("2006-01-02T15:04:05Z", st.LastJobAt)
		if err != nil {
			lastActivityItem.SetTitle("Última actividad: " + st.LastJobAt)
		} else {
			lastActivityItem.SetTitle("Última actividad: " + humanDuration(time.Since(t)))
		}
	}
}

// clickLoop escucha clicks en los items y dispatcha la acción.
func clickLoop() {
	for {
		select {
		case <-restartItem.ClickedCh:
			handleRestart()
		case <-logsItem.ClickedCh:
			handleLogs()
		case <-aboutItem.ClickedCh:
			handleAbout()
		case <-quitItem.ClickedCh:
			handleQuit()
			return
		}
	}
}

func handleRestart() {
	if err := ipc.RestartConnection(); err != nil {
		log.Printf("⚠ restart falló: %v", err)
		return
	}
	log.Println("✓ restart enviado al servicio")
	// Refresh inmediato para que el usuario vea el cambio.
	go func() {
		time.Sleep(500 * time.Millisecond)
		updateStatus()
	}()
}

// handleLogs abre el archivo de log con el handler default del SO
// (notepad en Windows, open en macOS, xdg-open en Linux).
func handleLogs() {
	path, err := logfile.Path()
	if err != nil {
		log.Printf("⚠ logs path: %v", err)
		return
	}
	if err := openExternal(path); err != nil {
		log.Printf("⚠ abrir logs: %v", err)
	}
}

func handleAbout() {
	info := fmt.Sprintf(
		"NovaSoft Print Agent (tray)\nVersión: %s",
		Version,
	)
	if lastStatus != nil {
		info += fmt.Sprintf(
			"\n\nAgent versión: %s\nAgent ID: %s\nBackend: %s\nImpresora: %s",
			lastStatus.Version,
			abbreviate(lastStatus.AgentID, 12),
			lastStatus.BackendURL,
			lastStatus.PrinterName,
		)
	}
	// Mostrar via msgbox externo. fyne.io/systray no expone diálogo
	// nativo — usamos cmd.exe + msg.exe o powershell para el msgbox.
	if err := showMessageBox("Acerca de NovaSoft Agent", info); err != nil {
		log.Printf("⚠ acerca de: %v", err)
	}
}

func handleQuit() {
	systray.Quit()
}

// humanDuration formatea una duración en español, granularidad de min.
// "hace 2 min" / "hace 1 hora" / "hace 3 días".
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return "hace menos de 1 min"
	}
	if d < time.Hour {
		return fmt.Sprintf("hace %d min", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("hace %d h", int(d.Hours()))
	}
	return fmt.Sprintf("hace %d días", int(d.Hours()/24))
}

// abbreviate trunca un string a maxLen agregando "…" si fue truncado.
// Útil para UUIDs largos en mensajes informativos.
func abbreviate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// openExternal abre un archivo o URL con el handler default del SO.
func openExternal(target string) error {
	cmd := exec.Command("cmd", "/c", "start", "", target)
	return cmd.Start()
}

// showMessageBox muestra un MessageBox nativo de Windows. Usamos
// PowerShell para no agregar dependencias Win32 directas en el tray.
// El comando es síncrono — el botón OK del usuario lo cierra.
func showMessageBox(title, body string) error {
	script := fmt.Sprintf(
		`Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show(%q, %q) | Out-Null`,
		body, title,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", script)
	return cmd.Start() // async — no bloqueamos el tray loop
}
