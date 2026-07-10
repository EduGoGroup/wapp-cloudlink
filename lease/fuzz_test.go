package lease

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// FuzzOpen ejercita el verificador del sobre firmado (open) con entradas
// arbitrarias (T8/H10): NINGÚN blob, por malformado que esté, debe provocar
// panic. open debe devolver siempre (claims, error) de forma controlada.
//
// El corpus semilla mezcla un sobre válido (para que el fuzzer explore mutaciones
// cercanas al formato real) con casos degenerados: JSON válido pero firma mala,
// vacío, no-JSON, tipos incorrectos y un blob truncado.
//
// Los casos F* corren como unit con el corpus en CI (go test). El fuzzing largo
// (go test -fuzz=FuzzOpen) es manual/on-demand.
func FuzzOpen(f *testing.F) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatalf("GenerateKey: %v", err)
	}

	valid, err := seal(priv, claims{EdgeID: "e", TenantID: "t", ExpiresUnix: 1_700_000_000, Counter: 1})
	if err != nil {
		f.Fatalf("seal: %v", err)
	}

	f.Add(valid)                                            // sobre válido y firmado
	f.Add([]byte(`{"claims":"eyJhIjoxfQ==","sig":"AAAA"}`)) // JSON válido, firma inválida
	f.Add([]byte(`{"claims":"","sig":""}`))                 // vacíos
	f.Add([]byte(`{}`))                                     // objeto vacío
	f.Add([]byte(``))                                       // bytes vacíos
	f.Add([]byte(`no soy json`))                            // no-JSON
	f.Add([]byte(`{"claims":123,"sig":"zz"}`))              // tipos incorrectos
	f.Add(valid[:len(valid)/2])                             // truncado

	f.Fuzz(func(t *testing.T, blob []byte) {
		// Contrato: no panic. El resultado (claims/error) es irrelevante para el
		// fuzz; solo importa que la función maneje cualquier entrada sin explotar.
		_, _ = open(pub, blob)
	})
}
