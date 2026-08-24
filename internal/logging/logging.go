// Package logging — salida del agent a archivo con rotación por tamaño.
//
// Motivación: como servicio de Windows no hay consola. Todo lo que hoy
// va a stdout/stderr se perdería y un técnico de soporte no tendría
// nada que mirar. Por eso el agent siempre escribe a
//
//	%PROGRAMDATA%\NovaSoftAgent\logs\agent.log
//
// En modo manual (`novasoft-agent run` en una consola) se escribe a
// consola Y a archivo, para que la experiencia interactiva de hoy no
// cambie pero el archivo igual quede para diagnóstico.
//
// La rotación es deliberadamente simple —por tamaño, sin compresión ni
// dependencias externas— porque el volumen de logs de un agent de
// impresión es de kilobytes por día:
//
//	agent.log      ← activo
//	agent.log.1    ← rotación más reciente
//	agent.log.2
//	agent.log.3    ← la más vieja; se borra en la siguiente rotación
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
)

const (
	// MaxSizeBytes — tamaño al que agent.log se rota.
	MaxSizeBytes = 5 * 1024 * 1024 // 5 MB

	// MaxBackups — cuántos archivos rotados se conservan además del activo.
	MaxBackups = 3

	logDirName  = "logs"
	logFileName = "agent.log"
)

// Dir devuelve el directorio de logs (no lo crea).
func Dir() (string, error) {
	base, err := config.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, logDirName), nil
}

// Path devuelve la ruta del log activo. Útil para que `service status`
// le diga al técnico dónde mirar.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, logFileName), nil
}

// Writer es un io.WriteCloser que rota el archivo al superar maxSize.
// Es seguro para uso concurrente: log.Logger puede escribir desde
// varias goroutines (el callback de impresión corre en la del SSE).
type Writer struct {
	path     string
	maxSize  int64
	maxFiles int

	mu   sync.Mutex
	file *os.File
	size int64
}

// NewWriter abre (o crea) el archivo de log en path, creando el
// directorio si hace falta. Escribe en modo append para no perder el
// historial entre reinicios del servicio.
func NewWriter(path string, maxSize int64, maxFiles int) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creando dir de logs: %w", err)
	}
	w := &Writer{path: path, maxSize: maxSize, maxFiles: maxFiles}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("abriendo %s: %w", w.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("stat %s: %w", w.path, err)
	}
	w.file = f
	w.size = info.Size()
	return nil
}

// Write implementa io.Writer, rotando antes de escribir si el archivo
// ya alcanzó maxSize.
//
// Un fallo de rotación no se propaga como error de escritura: preferimos
// un log que crece de más a un agent que deja de loguear (o peor, que
// falla) porque no pudo renombrar un archivo.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size+int64(len(p)) > w.maxSize {
		_ = w.rotate()
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate cierra el archivo activo, corre la cadena de backups y abre uno
// nuevo. Debe llamarse con w.mu tomado.
func (w *Writer) rotate() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}

	// La más vieja se descarta; el resto corre un puesto:
	// .2 → .3, .1 → .2, activo → .1
	oldest := fmt.Sprintf("%s.%d", w.path, w.maxFiles)
	_ = os.Remove(oldest)
	for i := w.maxFiles - 1; i >= 1; i-- {
		from := fmt.Sprintf("%s.%d", w.path, i)
		to := fmt.Sprintf("%s.%d", w.path, i+1)
		if _, err := os.Stat(from); err == nil {
			_ = os.Rename(from, to)
		}
	}
	if _, err := os.Stat(w.path); err == nil {
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			// No pudimos renombrar (¿archivo abierto por otro proceso?).
			// Reabrimos el mismo y seguimos: mejor loguear de más.
			return w.open()
		}
	}
	return w.open()
}

// Close cierra el archivo activo.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Setup redirige el logger estándar al archivo de log.
//
// Si alsoConsole es true (modo manual) la salida va a archivo Y a
// stderr. En modo servicio no hay consola, así que solo a archivo.
//
// Devuelve la ruta del log y un closer. Si el archivo no se puede abrir
// —por ejemplo un `run` manual de un usuario sin permiso de escritura en
// %PROGRAMDATA%— NO falla: deja el logger en consola y devuelve el
// error, para que el caller decida si es fatal. Como servicio sí lo es;
// a mano no.
func Setup(alsoConsole bool) (string, io.Closer, error) {
	log.SetFlags(log.LstdFlags)

	path, err := Path()
	if err != nil {
		return "", nil, err
	}
	w, err := NewWriter(path, MaxSizeBytes, MaxBackups)
	if err != nil {
		return path, nil, err
	}

	if alsoConsole {
		log.SetOutput(io.MultiWriter(os.Stderr, w))
	} else {
		log.SetOutput(w)
	}
	return path, w, nil
}
