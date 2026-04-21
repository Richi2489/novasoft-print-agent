// Package websocket — cliente del gateway de impresión de NovaSoft.
//
// Protocolo JSON (coincide con app/printing/websocket.py en el backend):
//
//   Agent -> Backend:
//     {"type": "ping"}
//     {"type": "job_result", "data": {"job_id": "...",
//                                     "status": "printed"|"failed",
//                                     "error_message": "..."}}
//
//   Backend -> Agent:
//     {"type": "pong"}
//     {"type": "print_job", "job_id": "...", "job_type": "...",
//                           "payload": {...}}
//     {"type": "disconnect", "reason": "..."}
//
// NOTA sobre "print_job": el backend NO envuelve el job en data — pone
// job_id/job_type/payload en el nivel top del mensaje. Adaptamos en
// handleMessage.
package websocket

import "encoding/json"

// Message es el sobre genérico. Backend -> Agent tiene type y a veces
// data (para pong, disconnect). Los print_job tienen campos top-level.
type Message struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
	// Campos top-level usados por print_job — redundantes con Data para
	// otros tipos, pero coinciden con el contrato del backend.
	JobID   string          `json:"job_id,omitempty"`
	JobType string          `json:"job_type,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// PrintJob es el evento que OnPrintJob recibe. El ID y JobType se
// exponen como strings; Payload queda crudo para que el caller lo
// delegue a printer.Convert sin doble-unmarshal.
type PrintJob struct {
	ID      string
	JobType string
	Payload json.RawMessage
}

// outgoing es la envoltura de mensajes que enviamos nosotros al backend.
// La usamos para ping y job_result. Separada de Message para no
// entreverar campos opcionales que el backend no lee.
type outgoing struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}
