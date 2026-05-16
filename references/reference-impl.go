// Package loghub_ident fornece a identidade do software dentro do ecossistema
// Loghub. Veja docs/SKILL-DEV.md para o contrato completo e docs/SPEC.md para
// detalhes por variável.
//
// IMPORTANTE: este arquivo é a IMPLEMENTAÇÃO DE REFERÊNCIA. Toda divergência
// de comportamento em relação à spec quebra o contrato com os consumidores
// da biblioteca. Em particular:
//   - Initialize não retorna error; falha => stderr + os.Exit.
//   - Initialize só pode ser chamada uma vez (exit 112 na segunda).
//   - Após Initialize, ID e seus campos são imutáveis.
//   - Sem dependências externas; apenas stdlib.
package loghub_ident

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Constantes públicas.
const (
	DefaultDataDir       = "/data"
	DefaultMachineIDFile = "/etc/machine-id"
)

// Nomes de arquivos internos e permissões.
const (
	fileAgentName    = "agent_name"
	fileAgentUUID    = "agent_uuid"
	fileWorkspace    = "workspace"
	uuidFilePermBits = 0o644
)

// Identity contém os seis valores de identidade do agente.
// Todos em lowercase. Imutável após Initialize.
type Identity struct {
	DataDir   string
	MachineID string
	AgentName string
	AgentUUID string
	Hostname  string
	Workspace string
}

// ID é o singleton populado por Initialize. Antes de Initialize é nil.
// Após Initialize, NÃO modificar. Sem mutex porque é write-once antes
// das goroutines do consumidor existirem.
var ID *Identity

// Regex pré-compilados (uso em hot path de Initialize).
var (
	reMachineID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	reAgentName = regexp.MustCompile(`^[a-z0-9._-]+$`)
	reHostname  = regexp.MustCompile(`^[a-z0-9.-]+$`)
	reWorkspace = regexp.MustCompile(`^[a-z0-9.-]+$`)
	reUUIDv4    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	// reAgentNameSanitize é aplicado APENAS no fallback argv[0], não em valores
	// vindos de env ou arquivo (esses precisam falhar com 104, não serem corrigidos
	// silenciosamente).
	reAgentNameSanitize = regexp.MustCompile(`[^a-z0-9._-]`)
)

// Initialize popula ID. Em qualquer falha, escreve no stderr e chama os.Exit
// com o código apropriado (ver docs/SKILL-DEV.md, seção "Códigos de saída").
//
// Não thread-safe. Deve ser chamada no início do main, antes de spawn de
// qualquer goroutine que vá ler ID.
func Initialize() {
	if ID != nil {
		fatal(112, "init", "Initialize() chamado mais de uma vez")
	}

	dataDir := resolveDataDir()
	machineID := resolveMachineID()
	agentName := resolveAgentName(dataDir)
	agentUUID := resolveAgentUUID(dataDir)
	hostname := resolveHostname()
	workspace := resolveWorkspace(dataDir)

	ID = &Identity{
		DataDir:   dataDir,
		MachineID: machineID,
		AgentName: agentName,
		AgentUUID: agentUUID,
		Hostname:  hostname,
		Workspace: workspace,
	}
}

// fatal escreve no stderr no formato padrão e encerra o processo.
func fatal(code int, variable, reason string) {
	fmt.Fprintf(os.Stderr, "lib-loghub-ident: %s: %s\n", variable, reason)
	os.Exit(code)
}

// readFirstLine retorna a primeira linha do arquivo já com whitespace trim.
// Arquivo vazio devolve ("", nil). Erro de I/O devolve ("", err).
func readFirstLine(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(data)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text), nil
}

// ----- generateUUIDv4 -----

// generateUUIDv4 gera um UUID v4 (RFC 4122) usando crypto/rand.
// Retorna o UUID no formato canônico: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
// onde y ∈ {8, 9, a, b}. Em caso de erro de leitura de entropia, retorna
// ("", error).
func generateUUIDv4() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("crypto/rand.Read falhou: %w", err)
	}
	// RFC 4122 §4.4: set version 4
	buf[6] = (buf[6] & 0x0f) | 0x40
	// RFC 4122 §4.4: set variant bits to 10xxxxxx
	buf[8] = (buf[8] & 0x3f) | 0x80

	var dst [36]byte
	hex.Encode(dst[0:8], buf[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], buf[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], buf[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], buf[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], buf[10:16])

	return string(dst[:]), nil
}

// ----- resolveMachineIDFile -----

// resolveMachineIDFile retorna o caminho do arquivo de machine-id.
// Usa a env MACHINE_ID_FILE se definida, caso contrário DefaultMachineIDFile.
func resolveMachineIDFile() string {
	v := strings.TrimSpace(os.Getenv("MACHINE_ID_FILE"))
	if v != "" {
		return v
	}
	return DefaultMachineIDFile
}

// ----- DATADIR -----

func resolveDataDir() string {
	dir := strings.TrimSpace(os.Getenv("DATADIR"))
	if dir == "" {
		dir = DefaultDataDir
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		fatal(100, "DATADIR", fmt.Sprintf("%q não existe ou não é um diretório", dir))
	}
	return dir
}

// ----- MACHINE_ID -----

func resolveMachineID() string {
	raw := strings.TrimSpace(os.Getenv("MACHINE_ID"))
	if raw == "" {
		midFile := resolveMachineIDFile()
		v, err := readFirstLine(midFile)
		if err != nil || v == "" {
			fatal(101, "MACHINE_ID", "env vazio e "+midFile+" ausente ou ilegível")
		}
		raw = v
	}
	raw = strings.ToLower(strings.ReplaceAll(raw, "-", ""))
	if !reMachineID.MatchString(raw) {
		fatal(102, "MACHINE_ID", fmt.Sprintf("%q não casa com ^[0-9a-f]{32}$", raw))
	}
	return raw
}

// ----- AGENT_NAME -----

func resolveAgentName(dataDir string) string {
	raw := strings.TrimSpace(os.Getenv("AGENT_NAME"))
	if raw == "" {
		if v, err := readFirstLine(filepath.Join(dataDir, fileAgentName)); err == nil && v != "" {
			raw = v
		}
	}
	if raw == "" {
		// Fallback: nome do executável. Aqui é OK sanear, porque o nome
		// veio do binário e não pode ser corrigido pelo operador.
		base := strings.ToLower(filepath.Base(os.Args[0]))
		base = reAgentNameSanitize.ReplaceAllString(base, "")
		if base == "" {
			fatal(103, "AGENT_NAME", "env vazio, arquivo ausente e argv[0] saneado ficou vazio")
		}
		raw = base
	}
	raw = strings.ToLower(raw)
	if !reAgentName.MatchString(raw) {
		fatal(104, "AGENT_NAME", fmt.Sprintf("%q não casa com ^[a-z0-9._-]+$", raw))
	}
	return raw
}

// ----- AGENT_UUID -----

func resolveAgentUUID(dataDir string) string {
	raw := strings.TrimSpace(os.Getenv("AGENT_UUID"))
	if raw == "" {
		if v, err := readFirstLine(filepath.Join(dataDir, fileAgentUUID)); err == nil && v != "" {
			raw = v
		}
	}
	if raw == "" {
		// Gerar UUID v4 localmente e gravar para persistência entre execuções.
		v, err := generateUUIDv4()
		if err != nil {
			fatal(105, "AGENT_UUID", fmt.Sprintf("geração de UUID v4 falhou: %v", err))
		}
		raw = v
		path := filepath.Join(dataDir, fileAgentUUID)
		if err := os.WriteFile(path, []byte(raw+"\n"), uuidFilePermBits); err != nil {
			fatal(106, "AGENT_UUID", fmt.Sprintf("gravação em %s falhou: %v", path, err))
		}
	}
	raw = strings.ToLower(raw)
	if !reUUIDv4.MatchString(raw) {
		fatal(107, "AGENT_UUID", fmt.Sprintf("%q não é um UUID v4 canônico", raw))
	}
	return raw
}

// ----- HOSTNAME -----

func resolveHostname() string {
	raw := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if raw == "" {
		v, err := os.Hostname()
		if err != nil {
			fatal(108, "HOSTNAME", fmt.Sprintf("os.Hostname() falhou: %v", err))
		}
		raw = v
	}
	raw = strings.ToLower(raw)
	if !reHostname.MatchString(raw) {
		fatal(109, "HOSTNAME", fmt.Sprintf("%q não casa com ^[a-z0-9.-]+$", raw))
	}
	return raw
}

// ----- WORKSPACE -----

func resolveWorkspace(dataDir string) string {
	raw := strings.TrimSpace(os.Getenv("WORKSPACE"))
	if raw == "" {
		path := filepath.Join(dataDir, fileWorkspace)
		v, err := readFirstLine(path)
		if err != nil || v == "" {
			fatal(110, "WORKSPACE", fmt.Sprintf("env vazio e %s ausente ou vazio", path))
		}
		raw = v
	}
	raw = strings.ToLower(raw)
	if !reWorkspace.MatchString(raw) {
		fatal(111, "WORKSPACE", fmt.Sprintf("%q não casa com ^[a-z0-9.-]+$", raw))
	}
	return raw
}
