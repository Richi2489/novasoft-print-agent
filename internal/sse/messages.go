// Package sse — cliente SSE del gateway de impresión.
//
// Reemplaza al package websocket (removido en el pivot del sprint
// 2026-04-21). Motivo del pivot: Railway + Fastly strippean el header
// Upgrade: websocket antes de llegar a gunicorn, así que WS nunca
// conectaba. SSE es HTTP/1.1 chunked puro — funciona a través de
// cualquier CDN sin config especial.
//
// Protocolo (coincide con app/printing/stream.py en el backend):
//
//   Backend → Agent (stream HTTP GET con Authorization: Bearer):
//     event: print_job
//     data: {"job_id":"...", "job_type":"...", "payload":{...}}
//     \n
//     : keepalive
//     \n
//
//   Agent → Backend (POST separado):
//     POST /printing/agents/jobs/{job_id}/result
//     Authorization: Bearer <token>
//     {"status":"printed|failed", "error_message":"..."}
package sse

import "encoding/json"

// PrintJob es el evento que OnPrintJob recibe. Payload queda crudo
// (json.RawMessage) para que el caller lo delegue directo a
// printer.Convert sin doble-unmarshal.
type PrintJob struct {
	ID      string          `json:"job_id"`
	JobType string          `json:"job_type"`
	Payload json.RawMessage `json:"payload"`
}
