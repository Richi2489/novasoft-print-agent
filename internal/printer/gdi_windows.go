//go:build windows

// Respaldo de impresión por GDI — el MISMO camino que la página de prueba
// de Windows (CreateDC → StartDoc → TextOut → EndDoc), pasando por el
// controlador de la impresora. Se usa sólo si SendRaw falla (feria Qro
// 2026-11-12: una laptop con Windows 11 rechazaba todo documento RAW con
// "The data is invalid" mientras la prueba de Windows sí salía).
//
// Con el controlador "Generic / Text Only" el texto llega tal cual a la
// térmica; con un controlador gráfico se dibuja con una fuente monoespaciada
// ajustada para que quepan LineWidth columnas en el ancho imprimible.
package printer

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	gdi32            = syscall.NewLazyDLL("gdi32.dll")
	procCreateDCW    = gdi32.NewProc("CreateDCW")
	procDeleteDC     = gdi32.NewProc("DeleteDC")
	procStartDocW    = gdi32.NewProc("StartDocW")
	procEndDoc       = gdi32.NewProc("EndDoc")
	procStartPage    = gdi32.NewProc("StartPage")
	procEndPage      = gdi32.NewProc("EndPage")
	procTextOutW     = gdi32.NewProc("TextOutW")
	procGetDeviceCap = gdi32.NewProc("GetDeviceCaps")
	procCreateFontW  = gdi32.NewProc("CreateFontW")
	procSelectObject = gdi32.NewProc("SelectObject")
	procDeleteObject = gdi32.NewProc("DeleteObject")
)

const (
	capHORZRES = 8
	capVERTRES = 10
)

type docInfoW struct {
	cbSize       int32
	lpszDocName  *uint16
	lpszOutput   *uint16
	lpszDatatype *uint16
	fwType       uint32
}

// SendText imprime renglones de texto por GDI en la impresora p.
func SendText(p *Printer, lineas []string) error {
	if p == nil {
		return fmt.Errorf("printer es nil")
	}
	driver, _ := syscall.UTF16PtrFromString("WINSPOOL")
	nombre, err := syscall.UTF16PtrFromString(p.Name)
	if err != nil {
		return err
	}
	hdc, _, e := procCreateDCW.Call(uintptr(unsafe.Pointer(driver)), uintptr(unsafe.Pointer(nombre)), 0, 0)
	if hdc == 0 {
		return fmt.Errorf("CreateDC %q: %v", p.Name, e)
	}
	defer procDeleteDC.Call(hdc)

	ancho, _, _ := procGetDeviceCap.Call(hdc, capHORZRES)
	alto, _, _ := procGetDeviceCap.Call(hdc, capVERTRES)
	if ancho == 0 {
		ancho = 576 // 80 mm a 203 dpi
	}
	// Fuente monoespaciada: el ancho de carácter ≈ 0.55 × alto en Consolas.
	altoLetra := int32(float64(ancho) / float64(LineWidth) / 0.55)
	if altoLetra < 8 {
		altoLetra = 8
	}
	fuente, _ := syscall.UTF16PtrFromString("Consolas")
	hfont, _, _ := procCreateFontW.Call(uintptr(altoLetra), 0, 0, 0, 400, 0, 0, 0,
		1 /*DEFAULT_CHARSET*/, 0, 0, 0, 1 /*FIXED_PITCH*/, uintptr(unsafe.Pointer(fuente)))
	if hfont != 0 {
		procSelectObject.Call(hdc, hfont)
		defer procDeleteObject.Call(hfont)
	}

	doc, _ := syscall.UTF16PtrFromString("NovaSoft Print Job")
	di := docInfoW{cbSize: int32(unsafe.Sizeof(docInfoW{})), lpszDocName: doc}
	if r, _, e := procStartDocW.Call(hdc, uintptr(unsafe.Pointer(&di))); int32(r) <= 0 {
		return fmt.Errorf("StartDoc: %v", e)
	}
	pagina := func() error {
		if r, _, e := procStartPage.Call(hdc); int32(r) <= 0 {
			return fmt.Errorf("StartPage: %v", e)
		}
		return nil
	}
	if err := pagina(); err != nil {
		procEndDoc.Call(hdc)
		return err
	}
	paso := int(altoLetra) + int(altoLetra)/5
	y := 0
	for _, l := range lineas {
		if alto > 0 && y+paso > int(alto) {
			procEndPage.Call(hdc)
			if err := pagina(); err != nil {
				procEndDoc.Call(hdc)
				return err
			}
			y = 0
		}
		if l != "" {
			u, _ := syscall.UTF16FromString(l)
			procTextOutW.Call(hdc, 0, uintptr(y), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1))
		}
		y += paso
	}
	procEndPage.Call(hdc)
	if r, _, e := procEndDoc.Call(hdc); int32(r) <= 0 {
		return fmt.Errorf("EndDoc: %v", e)
	}
	return nil
}
