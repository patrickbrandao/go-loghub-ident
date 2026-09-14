package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Testes de regressão permanentes de BUG-23 (arquivo de identidade vazio).

// TestFix_BUG23_EmptyIdentityFileDoesNotStallBoot: um machine_id ou agent_uuid
// pré-existente VAZIO (touch de provisionamento, cópia interrompida) fazia o
// processo cair no ramo de "corrida perdida" e esperar 10 s pela estabilização
// de um conteúdo que nunca viria. Um arquivo modificado há mais tempo que a
// janela de estabilização não é artefato de corrida: é regenerado na hora, com
// o aviso de descarte, e a identidade final é a que fica no disco.
func TestFix_BUG23_EmptyIdentityFileDoesNotStallBoot(t *testing.T) {
	for _, tc := range []struct{ file, env string }{
		{"agent_uuid", "AGENT_UUID"},
		{"machine_id", "MACHINE_ID"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			env := without(withDataDir(t), tc.env)
			dir := env["DATADIR"]
			path := filepath.Join(dir, tc.file)
			writeFile(t, dir, tc.file, "")
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}

			start := time.Now()
			res := runTimeout(t, env, 30*time.Second)
			elapsed := time.Since(start)
			if res.code != 0 {
				t.Fatalf("exit=%d\nstderr:\n%s", res.code, res.stderr)
			}
			if elapsed > 3*time.Second {
				t.Errorf("boot levou %s com um arquivo vazio antigo; não deveria esperar a estabilização", elapsed)
			}
			got := res.field(t, tc.env)
			if disk := readIdentFile(t, dir, tc.file); disk != got {
				t.Errorf("disco=%q, processo=%q", disk, got)
			}
			if !strings.Contains(res.stderr, "tinha conteúdo inválido (0 bytes") {
				t.Errorf("faltou o aviso de descarte:\n%s", res.stderr)
			}
		})
	}
}

// TestFix_BUG23_RecentEmptyFileStillWaitsToSettle preserva o outro lado
// (BUG-20): um arquivo vazio RECENTE pode ser o que um irmão acabou de publicar
// num volume de rede, e por isso continua recebendo a janela de estabilização —
// contada a partir do mtime, não do início do boot. Quando o conteúdo aparece,
// ele é adotado sem aviso e sem gerar identidade nova.
func TestFix_BUG23_RecentEmptyFileStillWaitsToSettle(t *testing.T) {
	if testing.Short() {
		t.Skip("espera parte da janela de estabilização")
	}
	env := without(withDataDir(t), "AGENT_UUID")
	dir := env["DATADIR"]
	path := filepath.Join(dir, "agent_uuid")
	writeFile(t, dir, "agent_uuid", "")
	// Faltam ~4 s de janela: o boot precisa esperar, mas não os 10 s inteiros.
	recent := time.Now().Add(-6 * time.Second)
	if err := os.Chtimes(path, recent, recent); err != nil {
		t.Fatal(err)
	}
	// O "irmão" publica o conteúdo válido 1 s depois.
	winner := "0190aaaa-bbbb-7ccc-8ddd-eeeeffff0000"
	go func() {
		time.Sleep(1 * time.Second)
		_ = os.WriteFile(path, []byte(winner+"\n"), 0o644)
	}()

	start := time.Now()
	res := runTimeout(t, env, 30*time.Second)
	elapsed := time.Since(start)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr:\n%s", res.code, res.stderr)
	}
	if got := res.field(t, "AGENT_UUID"); got != winner {
		t.Errorf("AGENT_UUID = %q (esperava adotar %q, publicado durante a espera)", got, winner)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("boot levou %s; deveria ter esperado a publicação", elapsed)
	}
	if res.stderr != "" {
		t.Errorf("nada a avisar: a identidade estabilizou; stderr:\n%s", res.stderr)
	}
}
