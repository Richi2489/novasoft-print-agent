// Package logfile configura el logger global del stdlib para escribir a
// %PROGRAMDATA%\NovaSoft\agent.log con rotación automática.
//
// Política de rotación (lumberjack):
//   - 10 MB por archivo activo.
//   - 5 backups históricos (.log.1, .log.2.gz, .log.3.gz, ...).
//   - Compresión gzip de archivos rotados.
//   - Sin time-based rotation — la rotación es por tamaño puro.
//
// Por qué lumberjack y no slog file handler nativo:
//   - lumberjack es estable, mantenido (gopkg.in/natefinch/lumberjack.v2),
//     y permite mezclar con io.MultiWriter para output dual a stderr.
//   - El stdlib `log` package es lo que el resto del agent ya usa
//     (no ameritan tocar 30+ call sites para migrar a slog).
package logfile

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/Richi2489/novasoft-print-agent/internal/paths"
	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	// LogFileName — nombre del archivo activo en DataDir.
	LogFileName = "agent.log"

	// MaxSizeMB activa la rotación cuando el log crece más allá de esto.
	MaxSizeMB = 10

	// MaxBackups — cuántos archivos rotados mantener antes de borrar
	// los más viejos.
	MaxBackups = 5
)

// Path devuelve la ruta absoluta al log activo.
func Path() (string, error) {
	dir, err := paths.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, LogFileName), nil
}

// Configure reemplaza el output del logger global por un escritor
// que rota agent.log automáticamente. Si alsoStderr es true (modo
// interactivo), las líneas también se imprimen a stderr.
//
// Idempotente — llamarlo múltiples veces solo cambia el output del
// logger global, no abre múltiples archivos al mismo tiempo (aunque
// los lumberjack.Logger anteriores quedan colgados sin GC inmediato;
// no es un problema porque solo se llama una vez al arranque).
func Configure(alsoStderr bool) error {
	dir, err := paths.EnsureDataDir()
	if err != nil {
		return fmt.Errorf("preparando dir de logs: %w", err)
	}

	rotator := &lumberjack.Logger{
		Filename:   filepath.Join(dir, LogFileName),
		MaxSize:    MaxSizeMB,
		MaxBackups: MaxBackups,
		Compress:   true,
		LocalTime:  true,
	}

	var writer io.Writer = rotator
	if alsoStderr {
		writer = io.MultiWriter(rotator, os.Stderr)
	}

	log.SetOutput(writer)
	// LstdFlags: "2009/01/23 01:23:23". Lmicroseconds para distinguir
	// eventos cercanos en debugging de race conditions.
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return nil
}
