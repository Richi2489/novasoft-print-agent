// Package sse — cliente SSE del gateway de impresión.
//
// Reemplaza al package websocket (removido en el pivot del sprint
// 2026-04-21). Motivo del pivot: Railway + Fastly strippean el header
// Upgrade: websocket antes de llegar a gunicorn, así que WS nunca
// conectaba. SSE es HTTP/1.1 chunked puro — funciona a través de
// cualquier CDN sin config especial.
//
// Protocolo v2 (desde v0.5.0; coincide con app/printing/stream.py y
// docs/printing/protocolo-v2.md del backend). Compatible con un backend v1:
//
//	Agent → Backend (antes de cada conexión):
//	  POST /printing/agents/hello
//	  {"protocol_version":2, "agent_version":"0.5.0", "recent_results":[...]}
//
//	Backend → Agent (stream HTTP GET con Authorization: Bearer y
//	X-NovaSoft-Print-Protocol: 2):
//	  event: print_job
//	  data: {"job_id":"...", "job_type":"...", "payload":{...},
//	         "delivery_key":"<job_id>:<n>", "delivery_attempt":1, "redelivery":false}
//	  \n
//	  : keepalive      (cada 15 s)
//	  \n
//
//	Agent → Backend (POST separado):
//	  POST /printing/agents/jobs/{job_id}/result
//	  {"status":"printed|failed", "error_message":"...", "delivery_key":"..."}
package sse

import "encoding/json"

// ProtocolVersion es la versión del protocolo que habla este agente.
const ProtocolVersion = 2

// PrintJob es el evento que OnPrintJob recibe. Payload queda crudo
// (json.RawMessage) para que el caller lo delegue directo a
// printer.Convert sin doble-unmarshal.
type PrintJob struct {
	ID      string          `json:"job_id"`
	JobType string          `json:"job_type"`
	Payload json.RawMessage `json:"payload"`

	// v2. Un backend v1 no los manda: DeliveryKey vacía → usar Key().
	DeliveryKey     string `json:"delivery_key,omitempty"`
	DeliveryAttempt int    `json:"delivery_attempt,omitempty"`
	Redelivery      bool   `json:"redelivery,omitempty"`
}

// Key es la clave de idempotencia de esta entrega. Con un backend v1 (sin
// delivery_key) cae al job_id: el mismo job no se imprime dos veces.
func (j *PrintJob) Key() string {
	if j.DeliveryKey != "" {
		return j.DeliveryKey
	}
	return j.ID
}

// RecentResult es un resultado del diario local que viaja en el saludo.
type RecentResult struct {
	JobID        string `json:"job_id"`
	DeliveryKey  string `json:"delivery_key,omitempty"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

// HelloRequest es el cuerpo de POST /printing/agents/hello.
type HelloRequest struct {
	ProtocolVersion int            `json:"protocol_version"`
	AgentVersion    string         `json:"agent_version"`
	RecentResults   []RecentResult `json:"recent_results"`
}

// HelloResponse es la respuesta del saludo.
type HelloResponse struct {
	OK              bool `json:"ok"`
	ProtocolVersion int  `json:"protocol_version"`
	AppliedResults  int  `json:"applied_results"`
	MaxRedeliveries int  `json:"max_redeliveries"`
}
