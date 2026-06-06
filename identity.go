package loghubident

import "regexp"

// DefaultDataDir é o caminho padrão do diretório de dados quando a variável de
// ambiente DATADIR está ausente ou vazia.
const DefaultDataDir = "/data"

// DefaultMachineIDFile é o caminho padrão do arquivo de machine-id quando a
// variável de ambiente MACHINE_ID_FILE está ausente ou vazia.
const DefaultMachineIDFile = "/etc/machine-id"

// DefaultWorkspace é o valor usado para WORKSPACE quando nenhuma fonte fornece
// um valor.
const DefaultWorkspace = "default"

// EnvDebug é a variável de ambiente que, quando definida (valor não vazio),
// ativa o log de diagnóstico em stderr durante Initialize.
const EnvDebug = "LOGHUB_IDENT_DEBUG"

// Nomes dos arquivos auto-geridos em $DATADIR e permissão de gravação.
const (
	fileMachineID = "machine_id"
	fileAgentName = "agent_name"
	fileAgentUUID = "agent_uuid"
	fileWorkspace = "workspace"

	filePerm = 0o644
)

// identity guarda os seis valores de identidade já resolvidos e validados.
// É um tipo NÃO exportado: o consumidor acessa os valores apenas pelos getters
// de pacote (DataDir, MachineID, etc.).
type identity struct {
	dataDir   string // diretório de dados resolvido (ex.: "/data")
	machineID string // 32 hex [0-9a-f], sem hífen
	agentName string // [a-z0-9._-]+
	agentUUID string // UUIDv7 canônico com hífens
	hostname  string // [a-z0-9.-]+
	workspace string // [a-z0-9.-]+

	// debug contém uma linha por campo descrevendo a origem do valor.
	// Só é emitido se a env LOGHUB_IDENT_DEBUG estiver definida.
	debug []string
}

// Regexes de validação, pré-compiladas uma única vez no carregamento do pacote.
var (
	reMachineID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	reAgentName = regexp.MustCompile(`^[a-z0-9._-]+$`)
	reAgentUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	reHostname  = regexp.MustCompile(`^[a-z0-9.-]+$`)
	reWorkspace = regexp.MustCompile(`^[a-z0-9.-]+$`)
)

// Variáveis de pacote que armazenam a identidade resolvida.
//
// São escritas uma única vez por Initialize (via apply) e nunca mais
// modificadas. Por contrato, Initialize roda antes de qualquer goroutine
// consumidora, o que estabelece a relação happens-before que torna a leitura
// pelos getters segura sem sincronização.
var (
	idDataDir   string
	idMachineID string
	idAgentName string
	idAgentUUID string
	idHostname  string
	idWorkspace string
)

// apply copia os valores resolvidos para as variáveis de pacote.
// Chamada exclusivamente por Initialize após uma resolução bem-sucedida.
func apply(id *identity) {
	idDataDir = id.dataDir
	idMachineID = id.machineID
	idAgentName = id.agentName
	idAgentUUID = id.agentUUID
	idHostname = id.hostname
	idWorkspace = id.workspace
}

// DataDir retorna o diretório de dados resolvido.
func DataDir() string { return idDataDir }

// MachineID retorna o identificador da máquina (32 hex, sem hífen).
func MachineID() string { return idMachineID }

// AgentName retorna o nome do agente.
func AgentName() string { return idAgentName }

// AgentUUID retorna o UUIDv7 canônico do agente (com hífens).
func AgentUUID() string { return idAgentUUID }

// Hostname retorna o hostname resolvido.
func Hostname() string { return idHostname }

// Workspace retorna o workspace (tenant) resolvido.
func Workspace() string { return idWorkspace }
