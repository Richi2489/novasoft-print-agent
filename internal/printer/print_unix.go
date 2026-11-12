//go:build !windows

// Envío de bytes crudos en macOS/Linux. Dos caminos:
//
//  1. Cola CUPS (USB o red ya agregada en Ajustes del Sistema):
//     `lp -d <cola> -o raw` con los bytes por stdin. "-o raw" le dice a
//     CUPS que NO pase el trabajo por filtros/driver — igual que el
//     StartRawDocument de Windows. Sin eso CUPS intentaría rasterizar el
//     ESC/POS y saldría basura o nada.
//  2. Impresora de red directa (Printer.Addr = "host:9100"): se abre un
//     socket TCP y se escriben los bytes (protocolo "raw"/JetDirect que
//     hablan prácticamente todas las térmicas Ethernet). No necesita
//     configurar nada en el Mac.
package printer

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// lpTimeout — lp sólo encola; si en 30 s no regresó, cupsd está mal.
	lpTimeout = 30 * time.Second
	// dialTimeout / writeTimeout — red local; una térmica responde en ms.
	dialTimeout  = 5 * time.Second
	writeTimeout = 20 * time.Second
)

// lpFn ejecuta lp con data por stdin. Variable para los tests.
var lpFn = func(queue string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), lpTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lp", "-d", queue, "-o", "raw", "-t", "NovaSoft Print Job")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("lp -d %s: %v: %s", queue, err, strings.TrimSpace(out.String()))
	}
	return nil
}

// SendRaw envía los bytes ESC/POS a la impresora sin transformarlos.
func SendRaw(p *Printer, data []byte) error {
	if p == nil {
		return fmt.Errorf("printer es nil")
	}
	if len(data) == 0 {
		return fmt.Errorf("data vacia — nada que imprimir")
	}
	if p.Addr != "" {
		return sendTCP(p.Addr, data)
	}
	if p.Name == "" {
		return fmt.Errorf("impresora sin nombre de cola")
	}
	return lpFn(p.Name, data)
}

// sendTCP escribe los bytes en host:puerto (impresora de red raw 9100).
func sendTCP(addr string, data []byte) error {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("conectando a la impresora de red %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(writeTimeout))
	n, err := conn.Write(data)
	if err != nil {
		return fmt.Errorf("escribiendo a %s: %w", addr, err)
	}
	if n != len(data) {
		return fmt.Errorf("write parcial a %s: %d de %d bytes", addr, n, len(data))
	}
	return nil
}
