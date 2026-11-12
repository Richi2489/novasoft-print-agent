package main

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/Richi2489/novasoft-print-agent/internal/config"
	"github.com/Richi2489/novasoft-print-agent/internal/printer"
)

// elegirImpresora decide a qué impresora va cada ticket.
//
// En macOS/Linux, si printer_name está fijado en el config (cola CUPS o
// destino de red tcp://IP:9100), gana. En Windows printer.Pinned siempre
// devuelve nil, así que el flujo es exactamente el de siempre:
// listar → error si no hay → SelectThermalPrinter.
func elegirImpresora(cfg *config.Config) (*printer.Printer, error) {
	if cfg != nil {
		if p := printer.Pinned(cfg.PrinterName); p != nil {
			log.Printf("→ Usando impresora fijada en config: %s", printer.Describe(p))
			if p.Addr == "" {
				avisarSiColaNoExiste(p.Name)
			}
			return p, nil
		}
	}

	printers, err := printer.ListSystem()
	if err != nil {
		return nil, fmt.Errorf("listando impresoras: %w", err)
	}
	if len(printers) == 0 {
		return nil, errors.New(printer.NoPrintersHint)
	}

	log.Println("Impresoras detectadas:")
	for i, p := range printers {
		log.Printf("  %d. %s", i+1, p.Name)
	}

	selected := printer.SelectThermalPrinter(printers)
	if selected == nil {
		return nil, fmt.Errorf("no pude seleccionar una impresora")
	}
	log.Printf("→ Usando: %s", selected.Name)
	return selected, nil
}

// avisarSiColaNoExiste deja rastro en el log si la cola fijada ya no
// está en CUPS (la renombraron o la borraron). No falla: lp dará el
// error concreto al imprimir.
func avisarSiColaNoExiste(nombre string) {
	lista, err := printer.ListSystem()
	if err != nil {
		return
	}
	for _, p := range lista {
		if p.Name == nombre {
			return
		}
	}
	log.Printf("⚠️  la cola %q no aparece en el sistema — revisa 'novasoft-agent printer list'", nombre)
}

// cmdPrinter — `printer list|set|clear`.
func cmdPrinter(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("falta el subcomando — usa: printer list | printer set <cola o IP:9100> | printer clear")
	}
	switch args[0] {
	case "list":
		lista, err := printer.ListSystem()
		if err != nil {
			return err
		}
		if len(lista) == 0 {
			fmt.Println("No hay impresoras en el sistema.")
			if runtime.GOOS != "windows" {
				fmt.Println("Si tu térmica es de red, fíjala por IP: novasoft-agent printer set 192.168.1.50:9100")
			}
			return nil
		}
		for i, p := range lista {
			fmt.Printf("  %d. %s\n", i+1, p.Name)
		}
		return nil

	case "set", "clear":
		if runtime.GOOS == "windows" {
			return fmt.Errorf("en Windows la impresora se elige sola (térmica detectada); este comando es para macOS/Linux")
		}
		cfg, err := config.Load()
		if err != nil {
			if errors.Is(err, config.ErrNotConfigured) {
				return fmt.Errorf("primero empareja: novasoft-agent pair")
			}
			return err
		}
		if args[0] == "clear" {
			cfg.PrinterName = ""
		} else {
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return fmt.Errorf("falta la impresora — ej: printer set POS_80   o   printer set 192.168.1.50:9100")
			}
			cfg.PrinterName = strings.TrimSpace(strings.Join(args[1:], " "))
		}
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("guardando config: %w", err)
		}
		if cfg.PrinterName == "" {
			fmt.Println("✓ Impresora fija borrada: el agente elegirá sola.")
		} else {
			fmt.Printf("✓ Impresora fijada: %s\n", printer.Describe(printer.Pinned(cfg.PrinterName)))
		}
		fmt.Println("  Si el agente ya estaba corriendo, reinícialo para que la tome.")
		return nil

	default:
		return fmt.Errorf("subcomando de printer desconocido: %s — usa: list|set|clear", args[0])
	}
}

// cmdTestPrint imprime un ticket de prueba local, sin pasar por el
// backend. `test-print [cola o IP:9100]`: sin argumento usa la misma
// impresora que usaría `run`.
func cmdTestPrint(args []string) error {
	var p *printer.Printer
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		p = printer.Pinned(strings.Join(args, " "))
		if p == nil { // Windows: nombre de impresora tal cual
			p = &printer.Printer{Name: strings.Join(args, " "), OS: runtime.GOOS}
		}
	} else {
		cfg, err := config.Load()
		if err != nil && !errors.Is(err, config.ErrNotConfigured) {
			return err
		}
		p, err = elegirImpresora(cfg)
		if err != nil {
			return err
		}
	}

	payload := printer.Payload{
		FormatVersion: "1",
		PaperWidthMM:  80,
		Sections: []printer.Section{
			{Type: "text", Content: "NovaSoft", Align: "center", Size: "large", Bold: true},
			{Type: "text", Content: "Prueba de impresión", Align: "center"},
			{Type: "separator"},
			{Type: "kv", Label: "Agente", Value: Version},
			{Type: "kv", Label: "Sistema", Value: runtime.GOOS + "/" + runtime.GOARCH},
			{Type: "kv", Label: "Fecha", Value: time.Now().Format("2006-01-02 15:04")},
			{Type: "separator"},
			{Type: "text", Content: "Acentos: áéíóú ñ ¡¿", Align: "center"},
			{Type: "feed", Lines: 3},
			{Type: "cut"},
		},
	}
	data, err := printer.Convert(payload)
	if err != nil {
		return fmt.Errorf("conversión ESC/POS: %w", err)
	}
	fmt.Printf("→ Imprimiendo prueba en %s…\n", printer.Describe(p))
	if err := printer.SendRaw(p, data); err != nil {
		return err
	}
	fmt.Println("✓ Enviado. Si no salió papel, revisa que la impresora esté encendida y con papel.")
	return nil
}
