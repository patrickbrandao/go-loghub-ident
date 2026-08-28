package loghubident

import "strings"

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

// Limites de comprimento dos campos textuais.
//
// maxFieldLen acomoda qualquer nome real de agente ou de tenant e mantém os
// valores utilizáveis como componente de caminho, rótulo de métrica ou chave de
// índice. Sem teto, uma variável de ambiente vaza um valor arbitrariamente
// grande para nomes de arquivo e cabeçalhos rio abaixo.
//
// maxHostnameLen e maxLabelLen vêm da RFC 1123.
const (
	maxFieldLen    = 64
	maxHostnameLen = 253
	maxLabelLen    = 63

	machineIDLen = 32 // 32 dígitos hexadecimais, sem hífen
	agentUUIDLen = 36 // UUID canônico com os 4 hífens
)

// Padrões documentados na SPEC. Não são compilados: existem apenas para compor
// as mensagens de erro, de modo que o operador leia exatamente o que a
// documentação promete. A validação é feita pelos validadores manuais abaixo.
const (
	patMachineID = `^[0-9a-f]{32}$`
	patAgentName = `^[a-z0-9._-]+$`
	patAgentUUID = `^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	patHostname  = `^[a-z0-9.-]+$`
	patWorkspace = `^[a-z0-9.-]+$`
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

	// debug contém uma linha por campo descrevendo a origem e o valor final.
	// Só é emitido se a env LOGHUB_IDENT_DEBUG estiver definida.
	debug []string

	// warnings contém eventos operacionais que o operador precisa ver mesmo
	// fora do modo debug — hoje, o descarte de uma identidade persistida.
	warnings []string
}

// ----- Validadores -----
//
// Substituem as regexes que antes eram compiladas na carga do pacote. Além de
// eliminarem o pacote regexp da árvore de dependências (~379 KB de binário e
// ~11,6 µs de init pagos por todo importador, mesmo o que nunca chama
// Initialize), permitem expressar as regras que uma classe de caracteres não
// alcança: comprimento máximo, recusa de componentes de caminho relativos
// ("." e "..") e a estrutura de rótulos da RFC 1123.

func isHexLower(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

func isAlnumLower(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')
}

// isRelPathComponent informa se o valor é um componente de caminho relativo.
// Aceitá-lo entregaria travessia de diretório a qualquer consumidor que use o
// campo para compor um caminho, uma chave de índice ou um tópico.
func isRelPathComponent(s string) bool {
	return s == "." || s == ".."
}

// validMachineID implementa ^[0-9a-f]{32}$.
func validMachineID(s string) bool {
	if len(s) != machineIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isHexLower(s[i]) {
			return false
		}
	}
	return true
}

// validAgentUUID implementa a regex estrita de UUIDv7 canônico:
// ^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$
func validAgentUUID(s string) bool {
	if len(s) != agentUUIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23: // posições dos hífens
			if c != '-' {
				return false
			}
		case 14: // versão: obrigatoriamente 7
			if c != '7' {
				return false
			}
		case 19: // variante RFC 9562: 8, 9, a ou b
			if c != '8' && c != '9' && c != 'a' && c != 'b' {
				return false
			}
		default:
			if !isHexLower(c) {
				return false
			}
		}
	}
	return true
}

// validAgentName implementa ^[a-z0-9._-]+$ com teto de comprimento e recusa de
// componentes de caminho relativos.
func validAgentName(s string) bool {
	if s == "" || len(s) > maxFieldLen || isRelPathComponent(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isAlnumLower(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// validWorkspace implementa ^[a-z0-9.-]+$ com as mesmas salvaguardas de
// validAgentName.
func validWorkspace(s string) bool {
	if s == "" || len(s) > maxFieldLen || isRelPathComponent(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isAlnumLower(c) && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

// validHostname implementa ^[a-z0-9.-]+$ acrescido das regras estruturais da
// RFC 1123: no máximo 253 caracteres, rótulos de 1 a 63 caracteres, nenhum
// rótulo vazio e nenhum hífen nas bordas de um rótulo. Sem isso, valores
// degenerados como "-", "..." ou "-host-" seriam aceitos e virariam chave de
// agrupamento rio abaixo.
func validHostname(s string) bool {
	if s == "" || len(s) > maxHostnameLen {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > maxLabelLen {
			return false
		}
		if !isAlnumLower(label[0]) || !isAlnumLower(label[len(label)-1]) {
			return false
		}
		for i := 0; i < len(label); i++ {
			if c := label[i]; !isAlnumLower(c) && c != '-' {
				return false
			}
		}
	}
	return true
}

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
