// Generador de iconos .ico para el tray del agent.
//
// Produce cmd/tray/icons/online.ico (verde) y cmd/tray/icons/offline.ico
// (rojo) en formato ICO 16x16 32-bit ARGB. Esos archivos se commitean
// al repo y se embeben en el tray con go:embed (el path tiene que ser
// adyacente al binary porque go:embed no soporta `..`).
//
// Ejecutar con:  go run ./tools/icongen
//
// Para regenerar los iconos (cambio de color, tamaño), modificar las
// variables de configuración y volver a correr.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

// Configuración de los iconos generados.
const (
	iconSize = 16 // 16x16 px — tamaño estándar para system tray Windows.
)

type rgba struct {
	r, g, b, a uint8
}

// Colores. Los valores están elegidos para ser visibles tanto en
// fondo claro (Windows 10/11 tray) como oscuro (high contrast mode).
var (
	colorOnline  = rgba{r: 0x10, g: 0xb9, b: 0x81, a: 0xff} // teal verde — coincide con NovaSoft brand
	colorOffline = rgba{r: 0xef, g: 0x44, b: 0x44, a: 0xff} // red 500 tailwind — clearly offline
	colorBorder  = rgba{r: 0x0f, g: 0x76, b: 0x6e, a: 0xff} // teal oscuro para outline
)

func main() {
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		os.Exit(1)
	}

	iconsDir := filepath.Join(repoRoot, "cmd", "tray", "icons")
	if err := os.MkdirAll(iconsDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "✗ creando %s: %v\n", iconsDir, err)
		os.Exit(1)
	}

	for name, c := range map[string]rgba{
		"online":  colorOnline,
		"offline": colorOffline,
	} {
		path := filepath.Join(iconsDir, name+".ico")
		if err := writeIcon(path, c); err != nil {
			fmt.Fprintf(os.Stderr, "✗ generando %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("✓ %s\n", path)
	}
}

// findRepoRoot sube directorios hasta encontrar go.mod, así el script
// funciona desde cualquier cwd.
func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod no encontrado subiendo desde %s", cwd)
		}
		dir = parent
	}
}

// writeIcon construye un ICO 16x16 con un círculo del color dado y
// outline más oscuro, transparente fuera del círculo. Salida en path.
//
// Layout del archivo ICO:
//
//	ICONDIR        (6 bytes)
//	ICONDIRENTRY   (16 bytes) × N (aquí N=1)
//	BITMAPINFOHEADER (40 bytes)
//	XOR mask       (width*height*4 bytes — pixels ARGB invertido a BGRA)
//	AND mask       (width*height/8 bytes — 1 bit por pixel; 0 = visible)
//
// Notas Windows:
//   - El height en BITMAPINFOHEADER es 2× el alto real (incluye el AND
//     mask como segunda mitad). En el ICO 16x16, height = 32.
//   - Los pixels van bottom-up (línea más baja primero).
//   - El AND mask es legacy de iconos 16-color; con BPP=32 + canal alpha
//     se ignora, pero algunos parsers Win32 lo requieren presente.
func writeIcon(path string, fill rgba) error {
	width, height := iconSize, iconSize

	// Generar pixel buffer ARGB (en memoria como [height][width]rgba).
	pixels := make([][]rgba, height)
	for y := 0; y < height; y++ {
		pixels[y] = make([]rgba, width)
	}

	// Dibujar círculo lleno con outline. Centro = (cx, cy), radio = r.
	cx, cy := float64(width-1)/2.0, float64(height-1)/2.0
	radius := float64(width)/2.0 - 0.5
	outlineThreshold := radius - 1.5

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := dx*dx + dy*dy
			r2 := radius * radius
			ot2 := outlineThreshold * outlineThreshold
			switch {
			case dist <= ot2:
				pixels[y][x] = fill
			case dist <= r2:
				pixels[y][x] = colorBorder
			default:
				// Transparente — alpha 0.
				pixels[y][x] = rgba{0, 0, 0, 0}
			}
		}
	}

	// Construir bytes de imagen (BMP DIB sin file header) — bottom-up.
	imgBuf := new(bytes.Buffer)

	// BITMAPINFOHEADER (40 bytes).
	bitmapInfo := struct {
		Size            uint32
		Width           int32
		Height          int32 // 2× para incluir AND mask
		Planes          uint16
		BitCount        uint16
		Compression     uint32
		SizeImage       uint32
		XPelsPerMeter   int32
		YPelsPerMeter   int32
		ClrUsed         uint32
		ClrImportant    uint32
	}{
		Size:     40,
		Width:    int32(width),
		Height:   int32(height * 2),
		Planes:   1,
		BitCount: 32,
	}
	if err := binary.Write(imgBuf, binary.LittleEndian, bitmapInfo); err != nil {
		return err
	}

	// XOR mask (BGRA, bottom-up).
	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			p := pixels[y][x]
			imgBuf.WriteByte(p.b)
			imgBuf.WriteByte(p.g)
			imgBuf.WriteByte(p.r)
			imgBuf.WriteByte(p.a)
		}
	}

	// AND mask (1 bit por pixel, 0 = visible). Aún con alpha en XOR,
	// Windows lo requiere presente. Dejamos todo en 0 (todo visible)
	// porque el alpha del XOR maneja la transparencia.
	andMaskBytes := width * height / 8
	for i := 0; i < andMaskBytes; i++ {
		imgBuf.WriteByte(0)
	}

	imgBytes := imgBuf.Bytes()

	// Construir el archivo ICO completo.
	icoBuf := new(bytes.Buffer)

	// ICONDIR.
	iconDir := struct {
		Reserved uint16
		Type     uint16
		Count    uint16
	}{0, 1, 1}
	if err := binary.Write(icoBuf, binary.LittleEndian, iconDir); err != nil {
		return err
	}

	// ICONDIRENTRY.
	iconEntry := struct {
		Width      uint8
		Height     uint8
		ColorCount uint8
		Reserved   uint8
		Planes     uint16
		BitCount   uint16
		BytesInRes uint32
		ImageOffset uint32
	}{
		Width:       uint8(width),
		Height:      uint8(height),
		ColorCount:  0,
		Reserved:    0,
		Planes:      1,
		BitCount:    32,
		BytesInRes:  uint32(len(imgBytes)),
		ImageOffset: 6 + 16, // después de ICONDIR + ICONDIRENTRY
	}
	if err := binary.Write(icoBuf, binary.LittleEndian, iconEntry); err != nil {
		return err
	}

	icoBuf.Write(imgBytes)

	return os.WriteFile(path, icoBuf.Bytes(), 0o644)
}
