//go:build integration

package loghub_ident_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var testBinary string

// TestMain compila um binário-fantoche que chama loghub_ident.Initialize()
// e imprime ID em JSON em stdout. Cada subteste roda o binário com env+volume
// controlados e checa exit code + stderr.
func TestMain(m *testing.M) {
	// Cria diretório temporário para o binário de teste.
	// Usa /tmp explicitamente porque o workspace pode ter espaços no path
	// (ex.: macOS iCloud) e go.mod replace não lida bem com isso.
	tmpDir, err := os.MkdirTemp("", "loghub_ident-integration-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "falha ao criar tmpdir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	testBinary = filepath.Join(tmpDir, "testbin")

	// Copia o módulo para um diretório sem espaços
	moduleRoot := findModuleRoot()
	moduleCopy := filepath.Join(tmpDir, "lib-loghub-ident")
	if err := copyDir(moduleRoot, moduleCopy); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao copiar módulo: %v\n", err)
		os.Exit(1)
	}

	// Diretório do fonte do binário-fantoche
	srcDir := filepath.Join(tmpDir, "src")
	os.MkdirAll(srcDir, 0755)

	// Escreve o main.go do binário-fantoche
	mainSrc := `package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/patrickbrandao/go-loghub-ident"
)

func main() {
	loghub_ident.Initialize()
	data, _ := json.Marshal(loghub_ident.ID)
	fmt.Println(string(data))
	os.Exit(0)
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(mainSrc), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao escrever main.go: %v\n", err)
		os.Exit(1)
	}

	// go.mod para o binário-fantoche — usa a cópia sem espaços
	goMod := `module testbin

go 1.21

require github.com/patrickbrandao/go-loghub-ident v0.0.0

replace github.com/patrickbrandao/go-loghub-ident => ` + moduleCopy + `
`
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(goMod), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao escrever go.mod: %v\n", err)
		os.Exit(1)
	}

	// Compila o binário
	cmd := exec.Command("go", "build", "-o", testBinary, ".")
	cmd.Dir = srcDir
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "falha ao compilar binário de teste: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// copyDir copia recursivamente um diretório, ignorando diretórios ocultos
// e arquivos de teste.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Caminho relativo
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		// Ignora diretórios ocultos, testbin, vendor, etc.
		if info.IsDir() {
			base := filepath.Base(path)
			if base != "." && strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			if base == "vendor" || base == "testbin" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0755)
		}

		// Copia o arquivo
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, info.Mode())
	})
}

// findModuleRoot retorna o caminho absoluto do diretório raiz do módulo
// (onde está go.mod).
func findModuleRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Fallback: assume que estamos dentro de loghub_ident/
	wd, _ := os.Getwd()
	return filepath.Dir(wd)
}

// runBin executa o binário de teste com o ambiente dado e retorna
// stdout, stderr e exit code.
func runBin(t *testing.T, env []string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(testBinary)
	cmd.Env = env

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("erro inesperado ao rodar binário: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// baseEnv retorna o ambiente mínimo para uma execução com sucesso.
func baseEnv(t *testing.T) (env []string, dataDir string) {
	t.Helper()
	dataDir = t.TempDir()

	// Cria arquivo workspace (obrigatório, sem fallback)
	os.WriteFile(filepath.Join(dataDir, "workspace"), []byte("test-ws\n"), 0644)

	env = []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=test-agent",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=test-host",
		"WORKSPACE=test-ws",
		"PATH=" + os.Getenv("PATH"),
	}
	return env, dataDir
}

// ---------------------------------------------------------------------------
// Testes de integração — um por código de saída
// ---------------------------------------------------------------------------

func TestIntegration_Exit100_DataDirNotExist(t *testing.T) {
	env := []string{
		"DATADIR=/nonexistent/path/that/does/not/exist",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 100 {
		t.Fatalf("exit code = %d, want 100; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "DATADIR:") {
		t.Fatalf("stderr não contém DATADIR: %s", stderr)
	}
}

func TestIntegration_Exit100_DataDirIsFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "afile")
	os.WriteFile(filePath, []byte("x"), 0644)

	env := []string{
		"DATADIR=" + filePath,
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 100 {
		t.Fatalf("exit code = %d, want 100; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "DATADIR:") {
		t.Fatalf("stderr não contém DATADIR: %s", stderr)
	}
}

func TestIntegration_Exit101_MachineIDMissing(t *testing.T) {
	dataDir := t.TempDir()
	os.WriteFile(filepath.Join(dataDir, "workspace"), []byte("ws\n"), 0644)

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=",
		"AGENT_NAME=test",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=test",
		"WORKSPACE=ws",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 101 {
		t.Fatalf("exit code = %d, want 101; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "MACHINE_ID:") {
		t.Fatalf("stderr não contém MACHINE_ID: %s", stderr)
	}
}

func TestIntegration_Exit102_MachineIDInvalid(t *testing.T) {
	dataDir := t.TempDir()

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=not-valid-hex",
		"AGENT_NAME=test",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=test",
		"WORKSPACE=ws",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 102 {
		t.Fatalf("exit code = %d, want 102; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "MACHINE_ID:") {
		t.Fatalf("stderr não contém MACHINE_ID: %s", stderr)
	}
}

func TestIntegration_Exit103_AgentNameEmpty(t *testing.T) {
	dataDir := t.TempDir()

	// Cria um symlink com nome que vira vazio após saneamento
	symlinkDir := t.TempDir()
	badName := filepath.Join(symlinkDir, "@@@@")
	os.Symlink(testBinary, badName)

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=test",
		"WORKSPACE=ws",
		"PATH=" + os.Getenv("PATH"),
	}
	cmd := exec.Command(badName)
	cmd.Env = env
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	if exitCode != 103 {
		t.Fatalf("exit code = %d, want 103; stderr=%s", exitCode, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "AGENT_NAME:") {
		t.Fatalf("stderr não contém AGENT_NAME: %s", errBuf.String())
	}
}

func TestIntegration_Exit104_AgentNameInvalid(t *testing.T) {
	dataDir := t.TempDir()

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=invalid name!!!",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=test",
		"WORKSPACE=ws",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 104 {
		t.Fatalf("exit code = %d, want 104; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "AGENT_NAME:") {
		t.Fatalf("stderr não contém AGENT_NAME: %s", stderr)
	}
}

func TestIntegration_Exit105_AgentUUIDGenerationFail(t *testing.T) {
	// Com a geração local via crypto/rand, é praticamente impossível
	// forçar uma falha de entropia de fora. Este código é coberto pelos
	// unit tests.
	t.Skip("crypto/rand.Read raramente falha; este código é coberto pelo unit test")
}

func TestIntegration_Exit106_AgentUUIDWriteFail(t *testing.T) {
	dataDir := t.TempDir()
	// Torna o diretório não gravável
	os.Chmod(dataDir, 0555)
	defer os.Chmod(dataDir, 0755)

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=test",
		"AGENT_UUID=",
		"HOSTNAME=test",
		"WORKSPACE=ws",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 106 {
		t.Fatalf("exit code = %d, want 106; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "AGENT_UUID:") {
		t.Fatalf("stderr não contém AGENT_UUID: %s", stderr)
	}
}

func TestIntegration_Exit107_AgentUUIDInvalid(t *testing.T) {
	dataDir := t.TempDir()

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=test",
		"AGENT_UUID=not-a-valid-uuid-at-all",
		"HOSTNAME=test",
		"WORKSPACE=ws",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 107 {
		t.Fatalf("exit code = %d, want 107; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "AGENT_UUID:") {
		t.Fatalf("stderr não contém AGENT_UUID: %s", stderr)
	}
}

func TestIntegration_Exit108_HostnameFail(t *testing.T) {
	// Difícil de forçar os.Hostname() a falhar de fora. Pulamos este teste
	// em ambientes onde o hostname está acessível (praticamente todos).
	t.Skip("os.Hostname() raramente falha; este código é coberto pelo unit test")
}

func TestIntegration_Exit109_HostnameInvalid(t *testing.T) {
	dataDir := t.TempDir()

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=test",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=invalid host!!!",
		"WORKSPACE=ws",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 109 {
		t.Fatalf("exit code = %d, want 109; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "HOSTNAME:") {
		t.Fatalf("stderr não contém HOSTNAME: %s", stderr)
	}
}

func TestIntegration_Exit110_WorkspaceMissing(t *testing.T) {
	dataDir := t.TempDir()

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=test",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=test",
		"WORKSPACE=",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 110 {
		t.Fatalf("exit code = %d, want 110; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "WORKSPACE:") {
		t.Fatalf("stderr não contém WORKSPACE: %s", stderr)
	}
}

func TestIntegration_Exit111_WorkspaceInvalid(t *testing.T) {
	dataDir := t.TempDir()

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=test",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=test",
		"WORKSPACE=invalid workspace!",
		"PATH=" + os.Getenv("PATH"),
	}
	_, stderr, code := runBin(t, env)
	if code != 111 {
		t.Fatalf("exit code = %d, want 111; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "WORKSPACE:") {
		t.Fatalf("stderr não contém WORKSPACE: %s", stderr)
	}
}

func TestIntegration_Exit112_DoubleInitialize(t *testing.T) {
	// Para testar dupla chamada, precisamos de um binário diferente que
	// chama Initialize() duas vezes.
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "doublebin")
	srcDir := filepath.Join(tmpDir, "src")
	os.MkdirAll(srcDir, 0755)

	mainSrc := `package main

import "github.com/patrickbrandao/go-loghub-ident"

func main() {
	loghub_ident.Initialize()
	loghub_ident.Initialize() // segunda chamada → exit 112
}
`
	os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(mainSrc), 0644)

	// Cria cópia do módulo sem espaços no path
	moduleCopyDir := filepath.Join(tmpDir, "modcopy")
	moduleRoot := findModuleRoot()
	if err := copyDir(moduleRoot, moduleCopyDir); err != nil {
		t.Fatalf("falha ao copiar módulo: %v", err)
	}

	goMod := `module testbin

go 1.21

require github.com/patrickbrandao/go-loghub-ident v0.0.0

replace github.com/patrickbrandao/go-loghub-ident => ` + moduleCopyDir + `
`
	os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(goMod), 0644)

	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = srcDir
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("falha ao compilar double-init binary: %v", err)
	}

	dataDir := t.TempDir()
	os.WriteFile(filepath.Join(dataDir, "workspace"), []byte("ws\n"), 0644)

	env := []string{
		"DATADIR=" + dataDir,
		"MACHINE_ID=abcdef0123456789abcdef0123456789",
		"AGENT_NAME=test",
		"AGENT_UUID=a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
		"HOSTNAME=test",
		"WORKSPACE=ws",
		"PATH=" + os.Getenv("PATH"),
	}

	cmd := exec.Command(binPath)
	cmd.Env = env
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	if exitCode != 112 {
		t.Fatalf("exit code = %d, want 112; stderr=%s", exitCode, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "Initialize()") {
		t.Fatalf("stderr não contém Initialize(): %s", errBuf.String())
	}
}

// ---------------------------------------------------------------------------
// Teste do caminho feliz completo
// ---------------------------------------------------------------------------

func TestIntegration_HappyPath(t *testing.T) {
	env, _ := baseEnv(t)
	stdout, stderr, code := runBin(t, env)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr)
	}

	var id struct {
		DataDir   string
		MachineID string
		AgentName string
		AgentUUID string
		Hostname  string
		Workspace string
	}
	if err := json.Unmarshal([]byte(stdout), &id); err != nil {
		t.Fatalf("JSON parseável falhou: %v\nstdout=%s", err, stdout)
	}

	if id.MachineID != "abcdef0123456789abcdef0123456789" {
		t.Fatalf("MachineID = %q", id.MachineID)
	}
	if id.AgentName != "test-agent" {
		t.Fatalf("AgentName = %q", id.AgentName)
	}
	if id.AgentUUID != "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d" {
		t.Fatalf("AgentUUID = %q", id.AgentUUID)
	}
	if id.Hostname != "test-host" {
		t.Fatalf("Hostname = %q", id.Hostname)
	}
	if id.Workspace != "test-ws" {
		t.Fatalf("Workspace = %q", id.Workspace)
	}
}
