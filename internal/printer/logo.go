package printer

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // registra decoder JPEG
	_ "image/png"  // registra decoder PNG
	"io"
	"net/http"
	"time"

	xdraw "golang.org/x/image/draw"
)

// logoHTTPTimeout — tope para descargar el logo. Si tarda más,
// abortamos y el ticket sale sin logo. Peor que un ticket feo es un
// ticket que no sale.
const logoHTTPTimeout = 5 * time.Second

// thermalDPI — resolución nominal de impresoras 80 mm estándar
// (GHIA GTP801, POS-80). Se usa para convertir mm → dots al redimensionar.
const thermalDPI = 203

// monoThreshold — luminancia (0..0xFFFF) por debajo de la cual un pixel
// se considera negro. 0x8000 = 50% es un compromiso razonable para
// logos planos. Para photos habría que hacer Floyd-Steinberg.
const monoThreshold = 0x8000

// LoadLogoESCPOS descarga una imagen del URL dado, la redimensiona a
// maxWidthMM de ancho (preservando aspect ratio), la convierte a
// bitmap monocromo, y devuelve los bytes ESC/POS listos para imprimir
// (GS v 0 raster bit image).
//
// Errores se devuelven al caller — el caller debe decidir si loguear y
// saltar el logo, o fallar el job. El patrón recomendado es skip con
// warning: imprimir sin logo es mejor UX que no imprimir.
//
// Formato del comando ESC/POS raster:
//
//	GS v 0 m xL xH yL yH d1...dN
//	  GS v 0    = 0x1D 0x76 0x30
//	  m         = 0 (normal), 1 (double-width), 2 (double-height), 3 (quad)
//	  xL xH     = width en BYTES (little-endian); each byte = 8 pixels
//	  yL yH     = height en DOTS
//	  d[]       = datos MSB-first, row-major, 1 = black dot.
//
// Nota: algunos firmwares tienen buffer chico y truncan imagenes > 255
// bytes de ancho; para 80 mm (320 dots = 40 bytes) nunca rozamos ese
// límite.
func LoadLogoESCPOS(url string, maxWidthMM int) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("url vacío")
	}
	if maxWidthMM <= 0 {
		maxWidthMM = 40 // default razonable para ticket 80 mm
	}

	// 1. Download.
	client := &http.Client{Timeout: logoHTTPTimeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "novasoft-print-agent")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	// Limitamos el body a 5 MB — un logo legítimo no pasa de cientos de
	// KB; más probable es que sea una URL equivocada devolviendo HTML.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// 2. Decode (PNG o JPEG — los dos formatos más comunes para logos).
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	// 3. Redimensionar preservando aspect ratio, alineando width al
	// siguiente múltiplo inferior de 8 (necesario para el pack a bytes).
	srcBounds := img.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW == 0 || srcH == 0 {
		return nil, fmt.Errorf("imagen con dimensiones inválidas")
	}

	// mm → dots a DPI nominal.
	targetWDots := maxWidthMM * thermalDPI / 25
	// Align down to multiple of 8 (pack MSB-first, cada byte = 8 cols).
	targetWDots = (targetWDots / 8) * 8
	if targetWDots < 8 {
		targetWDots = 8
	}
	targetHDots := srcH * targetWDots / srcW
	if targetHDots < 1 {
		targetHDots = 1
	}

	scaled := image.NewRGBA(image.Rect(0, 0, targetWDots, targetHDots))
	// BiLinear da buen resultado para logos flat con bordes limpios.
	// CatmullRom es más nítido pero más costoso — no vale el CPU para
	// un thumbnail que va a threshold monocromo.
	xdraw.BiLinear.Scale(scaled, scaled.Bounds(), img, srcBounds, xdraw.Over, nil)

	// 4. Threshold a monocromo + pack MSB-first.
	widthBytes := targetWDots / 8
	data := make([]byte, widthBytes*targetHDots)

	for y := 0; y < targetHDots; y++ {
		for x := 0; x < targetWDots; x++ {
			r, g, b, a := scaled.At(x, y).RGBA()
			// Transparent → tratar como blanco (fondo del papel).
			if a < 0x8000 {
				continue
			}
			// Luminancia perceptual (Rec. 601), valores 0..0xFFFF.
			gray := (299*r + 587*g + 114*b) / 1000
			if gray < monoThreshold {
				// Bit ON = dot negro. Byte layout MSB-first: pixel x=0
				// va al bit 7 del byte 0, x=1 al bit 6, etc.
				data[y*widthBytes+x/8] |= 1 << uint(7-x%8)
			}
		}
	}

	// 5. Ensamblar comando ESC/POS.
	var buf bytes.Buffer
	buf.Write([]byte{0x1D, 0x76, 0x30, 0x00}) // GS v 0 m=0
	buf.WriteByte(byte(widthBytes & 0xFF))
	buf.WriteByte(byte(widthBytes >> 8))
	buf.WriteByte(byte(targetHDots & 0xFF))
	buf.WriteByte(byte(targetHDots >> 8))
	buf.Write(data)

	return buf.Bytes(), nil
}
