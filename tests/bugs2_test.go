package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Testes de regressão dos defeitos catalogados em tasks/REPORT-02.md.

// ----- BUG-17: arquivos de identidade gravados sem fsync -----

// TestFix_BUG17_IdentityFilesAreComplete não consegue provocar um kernel panic,
// mas fixa o efeito observável da gravação durável e atômica: o arquivo
// persistido contém exatamente uma linha com o valor, sem resíduo de arquivo
// temporário deixado para trás no $DATADIR.
func TestFix_BUG17_IdentityFilesAreComplete(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID", "AGENT_UUID")
	dir := env["DATADIR"]

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}

	for name, want := range map[string]string{
		"machine_id": res.field(t, "MACHINE_ID"),
		"agent_uuid": res.field(t, "AGENT_UUID"),
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("leitura de %s: %v", name, err)
		}
		// Exatamente uma linha, terminada por um único "\n" (IMP-11).
		if string(data) != want+"\n" {
			t.Errorf("%s = %q (esperava %q)", name, string(data), want+"\n")
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("arquivo temporário deixado para trás em $DATADIR: %s", e.Name())
		}
	}
}

// ----- BUG-19: erro de I/O em $MACHINE_ID_FILE engolido silenciosamente -----

// TestFix_BUG19_MachineIDFileIOErrorReported: um $MACHINE_ID_FILE ilegível é
// uma instrução do operador que não pôde ser cumprida. Cair para o $DATADIR em
// silêncio esconde o erro de permissão e entrega outra identidade.
func TestFix_BUG19_MachineIDFileIOErrorReported(t *testing.T) {
	skipIfRoot(t)

	dir := t.TempDir()
	restricted := filepath.Join(dir, "mid")
	if err := os.WriteFile(restricted, []byte("abcdef0123456789abcdef0123456789\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(restricted, 0o644) })

	env := withDataDir(t)
	delete(env, "MACHINE_ID")
	env["MACHINE_ID_FILE"] = restricted

	checkFailure(t, run(t, env), 100, "MACHINE_ID_FILE")
}

// TestFix_BUG19_DefaultFileStaysBestEffort delimita o alcance da correção: o
// /etc/machine-id PADRÃO é uma fonte do sistema, não uma instrução do
// operador. Não existindo, a cadeia segue para o próximo nível sem abortar.
func TestFix_BUG19_DefaultFileStaysBestEffort(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID")
	delete(env, "MACHINE_ID_FILE") // usa o padrão do sistema

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if !reMachineID.MatchString(res.field(t, "MACHINE_ID")) {
		t.Errorf("MACHINE_ID = %q", res.field(t, "MACHINE_ID"))
	}
}
