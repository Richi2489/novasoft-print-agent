package main

import (
	"fmt"
	"testing"

	"github.com/Richi2489/novasoft-print-agent/internal/pairing"
)

// Los códigos de salida de `pair` son contrato con el instalador
// (build/installer/novasoft-agent-setup.iss, función MensajeDePair).
func TestPairExitCodeEsContratoConElInstalador(t *testing.T) {
	casos := map[error]int{
		pairing.ErrCodeInvalid: 2,
		pairing.ErrCodeUsed:    3,
		pairing.ErrCodeExpired: 4,
		fmt.Errorf("%w (dial tcp)", pairing.ErrNoNetwork): 5,
		fmt.Errorf("guardando config: acceso denegado"):    1,
	}
	for err, quiere := range casos {
		if got := pairExitCode(err); got != quiere {
			t.Errorf("pairExitCode(%v) = %d, quiere %d", err, got, quiere)
		}
	}
}
