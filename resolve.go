package loghubident

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// failure descreve uma falha de resolução: o código de saída do processo, a
// variável envolvida e o motivo legível. É retornado por resolve em vez de
// chamar os.Exit diretamente, para manter a lógica testável.
type failure struct {
	code     int
	variable string
	reason   string
}

func newFailure(code int, variable, reason string) *failure {
	return &failure{code: code, variable: variable, reason: reason}
}

// resolver carrega o estado da resolução de uma chamada a resolve.
type resolver struct {
	sys system

	dataDir string // caminho do DATADIR (resolvido cedo; validado lazy)
	dirOK   bool   // true após o DATADIR ter sido validado com sucesso

	debug []string // uma linha por campo, emitida apenas em modo debug
}

// resolve executa toda a resolução de identidade usando o system injetado.
// Retorna a identidade preenchida ou a primeira falha encontrada.
func resolve(sys system) (*identity, *failure) {
	r := &resolver{sys: sys}

	// O caminho do DATADIR é resolvido primeiro porque os demais campos podem
	// precisar dele. A VALIDAÇÃO do diretório, porém, é preguiçosa: só ocorre
	// quando um arquivo em $DATADIR precisa ser lido ou gravado.
	r.dataDir = r.resolveDataDirPath()

	machineID, f := r.resolveMachineID()
	if f != nil {
		return nil, f
	}
	agentName, f := r.resolveAgentName()
	if f != nil {
		return nil, f
	}
	agentUUID, f := r.resolveAgentUUID()
	if f != nil {
		return nil, f
	}
	hostname, f := r.resolveHostname()
	if f != nil {
		return nil, f
	}
	workspace, f := r.resolveWorkspace()
	if f != nil {
		return nil, f
	}

	return &identity{
		dataDir:   r.dataDir,
		machineID: machineID,
		agentName: agentName,
		agentUUID: agentUUID,
		hostname:  hostname,
		workspace: workspace,
		debug:     r.debug,
	}, nil
}

// logf adiciona uma linha de diagnóstico (emitida apenas em modo debug).
func (r *resolver) logf(format string, args ...any) {
	r.debug = append(r.debug, fmt.Sprintf(format, args...))
}

// ----- Helpers de baixo nível -----

// firstLine extrai a primeira linha de um conteúdo, com trim de whitespace.
func firstLine(data []byte) string {
	text := string(data)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

// readFile lê um arquivo arbitrário e devolve sua primeira linha (trimada).
// Arquivo ausente devolve ("", nil): tratado como fonte vazia, não como erro.
func (r *resolver) readFile(path string) (string, error) {
	data, err := r.sys.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return firstLine(data), nil
}

// ensureDataDir valida (uma única vez) que o DATADIR existe e é um diretório.
// Só deve ser chamada imediatamente antes de ler ou gravar um arquivo em
// $DATADIR — é o que torna a validação do DATADIR preguiçosa.
func (r *resolver) ensureDataDir() *failure {
	if r.dirOK {
		return nil
	}
	info, err := r.sys.Stat(r.dataDir)
	if err != nil || !info.IsDir() {
		return newFailure(100, "DATADIR",
			fmt.Sprintf("%q não existe ou não é um diretório", r.dataDir))
	}
	r.dirOK = true
	return nil
}

// readDataFile lê um arquivo dentro de $DATADIR, validando o diretório antes.
// Retorna (valor, falha). valor == "" indica fonte ausente/vazia.
func (r *resolver) readDataFile(name string) (string, *failure) {
	if f := r.ensureDataDir(); f != nil {
		return "", f
	}
	path := filepath.Join(r.dataDir, name)
	v, err := r.readFile(path)
	if err != nil {
		// Ausência do arquivo já é ("", nil) em readFile; aqui só chegam erros
		// de I/O legítimos (permissão negada, disco com falha, etc.). Esses não
		// devem ser silenciados como "fonte vazia" — mascarariam um problema
		// real do $DATADIR e dificultariam o diagnóstico. Abortam com 100.
		return "", newFailure(100, "DATADIR",
			fmt.Sprintf("leitura de %s falhou: %v", path, err))
	}
	return v, nil
}

// writeDataFile grava conteúdo em $DATADIR/name (validando o diretório antes).
// Em falha de gravação retorna uma failure com o código informado.
func (r *resolver) writeDataFile(name, content string, code int, variable string) *failure {
	if f := r.ensureDataDir(); f != nil {
		return f
	}
	path := filepath.Join(r.dataDir, name)
	if err := r.sys.WriteFile(path, []byte(content+"\n"), filePerm); err != nil {
		return newFailure(code, variable, fmt.Sprintf("gravação em %s falhou: %v", path, err))
	}
	return nil
}

// env retorna a variável de ambiente já com trim de whitespace.
func (r *resolver) env(key string) string {
	return strings.TrimSpace(r.sys.Getenv(key))
}

// ----- DATADIR -----

// resolveDataDirPath devolve apenas o CAMINHO do DATADIR (env ou padrão).
// Não valida a existência — isso é feito de forma preguiçosa por ensureDataDir.
func (r *resolver) resolveDataDirPath() string {
	dir := r.env("DATADIR")
	if dir == "" {
		return DefaultDataDir
	}
	return dir
}

// ----- MACHINE_ID -----

// normalizeMachineID aplica o saneamento específico do machine-id:
// lowercase e remoção de hífens.
func normalizeMachineID(raw string) string {
	return strings.ToLower(strings.ReplaceAll(raw, "-", ""))
}

// resolveMachineID implementa a cadeia de 4 níveis do MACHINE_ID:
// env → $MACHINE_ID_FILE → $DATADIR/machine_id → geração.
func (r *resolver) resolveMachineID() (string, *failure) {
	// Nível 1: env explícita. Presente e inválida aborta (102).
	if raw := r.env("MACHINE_ID"); raw != "" {
		v := normalizeMachineID(raw)
		if !reMachineID.MatchString(v) {
			return "", newFailure(102, "MACHINE_ID",
				fmt.Sprintf("%q não casa com ^[0-9a-f]{32}$", v))
		}
		r.logf("MACHINE_ID: env")
		return v, nil
	}

	// Nível 2: arquivo de machine-id do SO. Inválido aqui apenas CAI.
	midFile := r.env("MACHINE_ID_FILE")
	if midFile == "" {
		midFile = DefaultMachineIDFile
	} else if _, err := r.sys.Stat(midFile); err != nil {
		// Arquivo informado não existe: usar o padrão do sistema.
		midFile = DefaultMachineIDFile
	}
	if raw, err := r.readFile(midFile); err == nil && raw != "" {
		if v := normalizeMachineID(raw); reMachineID.MatchString(v) {
			r.logf("MACHINE_ID: file %s", midFile)
			return v, nil
		}
		// inválido: cai para o próximo nível
	}

	// Nível 3: arquivo auto-gerido em $DATADIR. Vazio/inválido CAI (regenera).
	v, f := r.readDataFile(fileMachineID)
	if f != nil {
		return "", f
	}
	if v != "" {
		if n := normalizeMachineID(v); reMachineID.MatchString(n) {
			r.logf("MACHINE_ID: file %s/%s", r.dataDir, fileMachineID)
			return n, nil
		}
		// inválido: cai para geração
	}

	// Nível 4: gerar a partir de um UUIDv7 (hífens removidos => 32 hex).
	uuid, err := r.sys.GenerateUUIDv7()
	if err != nil {
		return "", newFailure(105, "MACHINE_ID",
			fmt.Sprintf("geração de UUIDv7 base falhou: %v", err))
	}
	generated := normalizeMachineID(uuid)
	if !reMachineID.MatchString(generated) {
		return "", newFailure(102, "MACHINE_ID",
			fmt.Sprintf("valor gerado %q não casa com ^[0-9a-f]{32}$", generated))
	}
	if f := r.writeDataFile(fileMachineID, generated, 113, "MACHINE_ID"); f != nil {
		return "", f
	}
	r.logf("MACHINE_ID: generated")
	return generated, nil
}

// ----- AGENT_NAME -----

// resolveAgentName implementa a cadeia: env → $DATADIR/agent_name → argv[0].
func (r *resolver) resolveAgentName() (string, *failure) {
	// Nível 1: env. Presente e inválida aborta (104). Nunca grava arquivo.
	if raw := strings.ToLower(r.env("AGENT_NAME")); raw != "" {
		if !reAgentName.MatchString(raw) {
			return "", newFailure(104, "AGENT_NAME",
				fmt.Sprintf("%q não casa com ^[a-z0-9._-]+$", raw))
		}
		r.logf("AGENT_NAME: env")
		return raw, nil
	}

	// Nível 2: arquivo. Presente e inválido aborta (104).
	v, f := r.readDataFile(fileAgentName)
	if f != nil {
		return "", f
	}
	if v != "" {
		v = strings.ToLower(v)
		if !reAgentName.MatchString(v) {
			return "", newFailure(104, "AGENT_NAME",
				fmt.Sprintf("%q não casa com ^[a-z0-9._-]+$", v))
		}
		r.logf("AGENT_NAME: file %s/%s", r.dataDir, fileAgentName)
		return v, nil
	}

	// Nível 3: nome base de argv[0], lowercase, sem o sufixo ".exe".
	args := r.sys.Args()
	argv0 := ""
	if len(args) > 0 {
		argv0 = args[0]
	}
	base := strings.ToLower(filepath.Base(argv0))
	base = strings.TrimSuffix(base, ".exe")
	if base == "" || base == "." || base == "/" {
		return "", newFailure(103, "AGENT_NAME",
			"env vazia, arquivo ausente e argv[0] ficou vazio")
	}
	if !reAgentName.MatchString(base) {
		return "", newFailure(104, "AGENT_NAME",
			fmt.Sprintf("%q (de argv[0]) não casa com ^[a-z0-9._-]+$", base))
	}
	r.logf("AGENT_NAME: fallback argv[0]")
	return base, nil
}

// ----- AGENT_UUID -----

// resolveAgentUUID implementa a cadeia: env → $DATADIR/agent_uuid → geração.
func (r *resolver) resolveAgentUUID() (string, *failure) {
	// Nível 1: env. Presente e inválida aborta (107). Nunca grava arquivo.
	if raw := strings.ToLower(r.env("AGENT_UUID")); raw != "" {
		if !reAgentUUID.MatchString(raw) {
			return "", newFailure(107, "AGENT_UUID",
				fmt.Sprintf("%q não é um UUIDv7 canônico", raw))
		}
		r.logf("AGENT_UUID: env")
		return raw, nil
	}

	// Nível 2: arquivo auto-gerido. Vazio/inválido CAI (regenera).
	v, f := r.readDataFile(fileAgentUUID)
	if f != nil {
		return "", f
	}
	if v != "" {
		v = strings.ToLower(v)
		if reAgentUUID.MatchString(v) {
			r.logf("AGENT_UUID: file %s/%s", r.dataDir, fileAgentUUID)
			return v, nil
		}
		// inválido: cai para geração
	}

	// Nível 3: gerar UUIDv7 e persistir.
	uuid, err := r.sys.GenerateUUIDv7()
	if err != nil {
		return "", newFailure(105, "AGENT_UUID",
			fmt.Sprintf("geração de UUIDv7 falhou: %v", err))
	}
	uuid = strings.ToLower(strings.TrimSpace(uuid))
	if !reAgentUUID.MatchString(uuid) {
		return "", newFailure(107, "AGENT_UUID",
			fmt.Sprintf("valor gerado %q não é um UUIDv7 canônico", uuid))
	}
	if f := r.writeDataFile(fileAgentUUID, uuid, 106, "AGENT_UUID"); f != nil {
		return "", f
	}
	r.logf("AGENT_UUID: generated")
	return uuid, nil
}

// ----- HOSTNAME -----

// resolveHostname implementa a cadeia: env → os.Hostname().
func (r *resolver) resolveHostname() (string, *failure) {
	raw := r.env("HOSTNAME")
	origin := "env"
	if raw == "" {
		v, err := r.sys.Hostname()
		if err != nil {
			return "", newFailure(108, "HOSTNAME",
				fmt.Sprintf("os.Hostname() falhou: %v", err))
		}
		raw = strings.TrimSpace(v)
		origin = "os.Hostname"
	}
	raw = strings.ToLower(raw)
	if !reHostname.MatchString(raw) {
		return "", newFailure(109, "HOSTNAME",
			fmt.Sprintf("%q não casa com ^[a-z0-9.-]+$", raw))
	}
	r.logf("HOSTNAME: %s", origin)
	return raw, nil
}

// ----- WORKSPACE -----

// resolveWorkspace implementa a cadeia: env → $DATADIR/workspace → "default".
func (r *resolver) resolveWorkspace() (string, *failure) {
	// Nível 1: env. Presente e inválida aborta (111).
	if raw := strings.ToLower(r.env("WORKSPACE")); raw != "" {
		if !reWorkspace.MatchString(raw) {
			return "", newFailure(111, "WORKSPACE",
				fmt.Sprintf("%q não casa com ^[a-z0-9.-]+$", raw))
		}
		r.logf("WORKSPACE: env")
		return raw, nil
	}

	// Nível 2: arquivo. Presente e inválido aborta (111).
	v, f := r.readDataFile(fileWorkspace)
	if f != nil {
		return "", f
	}
	if v != "" {
		v = strings.ToLower(v)
		if !reWorkspace.MatchString(v) {
			return "", newFailure(111, "WORKSPACE",
				fmt.Sprintf("%q não casa com ^[a-z0-9.-]+$", v))
		}
		r.logf("WORKSPACE: file %s/%s", r.dataDir, fileWorkspace)
		return v, nil
	}

	// Nível 3: fallback determinístico (não grava arquivo).
	r.logf("WORKSPACE: fallback %q", DefaultWorkspace)
	return DefaultWorkspace, nil
}
