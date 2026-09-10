package loghubident

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
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

	dataDir    string // caminho do DATADIR (resolvido cedo; existência validada lazy)
	dirChecked bool   // true após o primeiro Stat do DATADIR (evita re-Stat)
	dirErr     error  // nil = utilizável; fs.ErrNotExist = ausente; errNotDir = não é diretório; outro = erro real

	debug    []string // uma linha por campo, emitida apenas em modo debug
	warnings []string // eventos operacionais, emitidos SEMPRE
}

// resolve executa toda a resolução de identidade usando o system injetado.
// Retorna a identidade preenchida ou a primeira falha encontrada.
//
// Mesmo em falha o *identity devolvido é não-nulo e carrega o diagnóstico
// acumulado até ali (debug e warnings). É justamente no caminho de erro que
// essas linhas mais servem ao operador, então elas não podem ser descartadas.
func resolve(sys system) (*identity, *failure) {
	r := &resolver{sys: sys}

	partial := func(f *failure) (*identity, *failure) {
		return &identity{debug: r.debug, warnings: r.warnings}, f
	}

	// O caminho do DATADIR é resolvido primeiro porque os demais campos podem
	// precisar dele. A validação da EXISTÊNCIA, porém, é preguiçosa: só ocorre
	// quando um arquivo em $DATADIR precisa ser realmente gravado.
	if f := r.resolveDataDirPath(); f != nil {
		return partial(f)
	}

	machineID, f := r.resolveMachineID()
	if f != nil {
		return partial(f)
	}
	agentName, f := r.resolveAgentName()
	if f != nil {
		return partial(f)
	}
	agentUUID, f := r.resolveAgentUUID()
	if f != nil {
		return partial(f)
	}
	hostname, f := r.resolveHostname()
	if f != nil {
		return partial(f)
	}
	workspace, f := r.resolveWorkspace()
	if f != nil {
		return partial(f)
	}

	return &identity{
		dataDir:   r.dataDir,
		machineID: machineID,
		agentName: agentName,
		agentUUID: agentUUID,
		hostname:  hostname,
		workspace: workspace,
		debug:     r.debug,
		warnings:  r.warnings,
	}, nil
}

// logf adiciona uma linha de diagnóstico (emitida apenas em modo debug).
func (r *resolver) logf(format string, args ...any) {
	r.debug = append(r.debug, fmt.Sprintf(format, args...))
}

// warnf registra um evento operacional que o operador precisa ver MESMO fora do
// modo debug. Descartar uma identidade persistida é o caso central: sem aviso,
// um agente volta com outro machine_id depois de um crash e aparece no servidor
// como uma máquina nova, sem nenhum rastro que permita correlacionar.
func (r *resolver) warnf(format string, args ...any) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

// ----- Helpers de baixo nível -----

// bomUTF8 é a marca de ordem de bytes que editores do Windows escrevem por
// padrão no início do arquivo.
var bomUTF8 = []byte{0xEF, 0xBB, 0xBF}

// firstLine extrai a primeira linha de um conteúdo e a sanea: remove o BOM
// UTF-8, corta na primeira quebra de linha e apara espaços E caracteres de
// controle das bordas.
//
// Os caracteres de controle importam mais do que parecem: um byte NUL residual
// — exatamente o que uma gravação truncada por crash deixa — sobreviveria ao
// TrimSpace, reprovaria na validação e faria a lib DESCARTAR a identidade
// persistida. O trabalho é feito sobre []byte para não copiar todo o conteúdo
// lido para o heap antes de recortar a primeira linha.
func firstLine(data []byte) string {
	data = bytes.TrimPrefix(data, bomUTF8)
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		data = data[:i]
	}
	return string(bytes.TrimFunc(data, isSpaceOrControl))
}

func isSpaceOrControl(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

// fnv1a64 devolve o hash FNV-1a de 64 bits de s, usado para identificar
// conteúdo inválido sem reproduzir nenhum byte do conteúdo em mensagens.
func fnv1a64(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

// describeInvalid descreve um valor reprovado SEM reproduzi-lo. O conteúdo de
// um arquivo em $DATADIR pode ter sido plantado por outro inquilino do volume
// (um symlink para um segredo, por exemplo); ecoá-lo em stderr entregaria o
// arquivo ao coletor de logs. O tamanho e o hash FNV-1a de 64 bits identificam
// a ocorrência de forma determinística e não-invertível (SPEC §12).
func describeInvalid(v string) string {
	return fmt.Sprintf("%d bytes, hash %016x", len(v), fnv1a64(v))
}

// maxEchoLen limita o que uma mensagem reproduz de um valor de ENV — que é do
// operador, mas pode ser enorme ou ter sido colado por engano.
const maxEchoLen = 64

func preview(v string) string {
	if len(v) <= maxEchoLen {
		return fmt.Sprintf("%q", v)
	}
	return fmt.Sprintf("%q… (%d bytes)", v[:maxEchoLen], len(v))
}

// readFile lê um arquivo do SISTEMA (fora de $DATADIR) e devolve sua primeira
// linha (saneada). readDataPath faz o mesmo para um caminho DENTRO de $DATADIR,
// sem seguir links simbólicos.
func (r *resolver) readFile(path string) (string, error) {
	return r.readWith(r.sys.ReadFile, path)
}

func (r *resolver) readDataPath(path string) (string, error) {
	data, err := r.sys.ReadFileNoFollow(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		// Fontes inválidas em $DATADIR (symlinks, FIFOs, > 4 KiB) não são
		// engolidas como ausência: abortam com código 100 conforme a SPEC §13.
		return "", err
	}
	return firstLine(data), nil
}

func (r *resolver) readWith(read func(string) ([]byte, error), path string) (string, error) {
	data, err := read(path)
	if err != nil {
		// errors.Is desembrulha erros anotados com %w — ao contrário de
		// os.IsNotExist, que só entende *PathError direto e converteria
		// "arquivo ausente" em aborto para qualquer system alternativo.
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		if errors.Is(err, errInvalidSource) {
			r.logf("fonte %s ignorada: %v", path, err)
			return "", nil
		}
		return "", err
	}
	return firstLine(data), nil
}

// errNotDir marca um $DATADIR que existe, mas não é um diretório.
var errNotDir = errors.New("não é um diretório")

// checkDataDir inspeciona $DATADIR uma única vez e CLASSIFICA o resultado, em
// vez de reduzi-lo a um booleano. "Ausente" é fs.ErrNotExist e nada mais — o
// mesmo critério de machineIDFilePath (SPEC §7.2): permissão negada no
// diretório pai, ELOOP, EIO ou ENAMETOOLONG são erros reais que o operador
// precisa ver, não uma "fonte ausente".
func (r *resolver) checkDataDir() error {
	if r.dirChecked {
		return r.dirErr
	}
	r.dirChecked = true
	info, err := r.sys.Stat(r.dataDir)
	switch {
	case err != nil:
		r.dirErr = err
	case !info.IsDir():
		r.dirErr = errNotDir
	}
	return r.dirErr
}

// dataDirFailure converte o erro classificado numa falha 100 com a causa real.
func (r *resolver) dataDirFailure(err error) *failure {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return newFailure(100, "DATADIR", fmt.Sprintf("%q não existe", r.dataDir))
	case errors.Is(err, errNotDir):
		return newFailure(100, "DATADIR", fmt.Sprintf("%q não é um diretório", r.dataDir))
	default:
		return newFailure(100, "DATADIR", fmt.Sprintf("%q inacessível: %v", r.dataDir, err))
	}
}

// ensureDataDir valida que $DATADIR existe e é um diretório. Só deve ser
// chamada imediatamente antes de GRAVAR: gerar uma identidade sem onde
// persistí-la é uma falha legítima (SPEC §6).
func (r *resolver) ensureDataDir() *failure {
	if err := r.checkDataDir(); err != nil {
		return r.dataDirFailure(err)
	}
	return nil
}

// readDataFile lê um arquivo dentro de $DATADIR.
// Retorna (valor, falha). valor == "" indica fonte ausente/vazia.
func (r *resolver) readDataFile(name string) (string, *failure) {
	if err := r.checkDataDir(); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.logf("$DATADIR %q inexistente: fonte %s ignorada", r.dataDir, name)
			return "", nil
		}
		return "", r.dataDirFailure(err)
	}
	path := filepath.Join(r.dataDir, name)
	v, err := r.readDataPath(path)
	if err != nil {
		// Ausência do arquivo já é ("", nil) em readWith; aqui só chegam erros
		// de I/O legítimos ou fontes inválidas (symlinks, etc.). Abortam com 100.
		return "", newFailure(100, "DATADIR",
			fmt.Sprintf("leitura de %s falhou: %v", path, err))
	}
	return v, nil
}

// identLine normaliza o conteúdo a gravar: exatamente uma linha, terminada por
// um único "\n" (sem linha em branco extra se o valor já vier com quebra).
func identLine(content string) []byte {
	return []byte(strings.TrimRight(content, "\r\n") + "\n")
}

// identKind reúne o que difere entre os dois arquivos auto-geridos, para que a
// lógica de persistência — a parte sensível a concorrência — seja UMA só. Foi
// justamente a duplicação dessa lógica entre MACHINE_ID e AGENT_UUID que deixou
// um dos dois caminhos sem convergência.
type identKind struct {
	file      string              // nome do arquivo em $DATADIR
	normalize func(string) string // saneamento específico do campo
	valid     func(string) bool   // validação específica do campo
	writeCode int                 // código de saída para falha de gravação
	variable  string              // nome da variável nas mensagens de erro
}

var (
	kindMachineID = identKind{fileMachineID, normalizeMachineID, validMachineID, 113, "MACHINE_ID"}
	kindAgentUUID = identKind{fileAgentUUID, strings.ToLower, validAgentUUID, 106, "AGENT_UUID"}
)

// regenSuffix nomeia o registro de regeneração de um arquivo auto-gerido, e
// regenAttempts limita as tentativas de obtê-lo.
// Janela do plano B de CreateExclusive (filesystem sem hard link): o arquivo
// existe ANTES de ter conteúdo se o escritor for lento. Quem perde a corrida
// e lê vazio ou parcial não pode concluir "corrompido" de imediato.
// Relemos por um tempo limitado antes de declarar corrupção.
var (
	// settleAttempts * settleInterval = 10 s de espera máxima antes de declarar
	// corrupção. Em volumes de rede (NFS) ou no plano B de CreateExclusive
	// (cujo claimTTL é 10 s), o perdedor da corrida não pode descartar
	// precipitadamente o arquivo enquanto o vencedor ainda grava/sincroniza.
	settleAttempts = 500
	settleInterval = 20 * time.Millisecond
	settleSleep    = time.Sleep
)

// readSettled relê path enquanto o conteúdo não for válido para k, até o teto.
func (r *resolver) readSettled(path string, k identKind) (string, error) {
	for i := 0; ; i++ {
		raw, err := r.readDataPath(path)
		if err != nil || k.valid(k.normalize(raw)) || i >= settleAttempts {
			return raw, err
		}
		settleSleep(settleInterval)
	}
}

// persistOrigin diz de onde saiu o valor que persistGenerated mandou adotar.
type persistOrigin int

const (
	regenSuffix   = ".regen"
	regenAttempts = 3

	originCreated     persistOrigin = iota // arquivo criado do zero por ESTE processo
	originRace                             // arquivo criado por outro processo nesta subida
	originRegenerated                      // ESTE processo venceu a regeneração: valor novo
	originRecord                           // registro já existente — desta subida OU de recuperação anterior
)

// persistGenerated persiste um valor recém-gerado em $DATADIR e devolve o valor
// que deve ser ADOTADO — que pode ser o de outro processo, quando vários sobem
// ao mesmo tempo sobre o mesmo volume. corrupt informa que o arquivo já existe
// com conteúdo inválido e precisa ser substituído.
func (r *resolver) persistGenerated(k identKind, value string, corrupt bool) (string, persistOrigin, *failure) {
	if f := r.ensureDataDir(); f != nil {
		return "", originCreated, f
	}
	path := filepath.Join(r.dataDir, k.file)
	regen := filepath.Join(r.dataDir, "."+k.file+regenSuffix)

	if !corrupt {
		created, err := r.sys.CreateExclusive(path, identLine(value), filePerm)
		if err != nil {
			return "", originCreated, newFailure(k.writeCode, k.variable,
				fmt.Sprintf("gravação em %s falhou: %v", path, err))
		}
		if created {
			// Identidade nova a partir do zero: um registro de regeneração
			// anterior está obsoleto.
			_ = r.sys.Remove(regen)
			return value, originCreated, nil
		}
		// Perdemos a corrida: adotamos o valor de quem criou o arquivo.
		adopted, err := r.readSettled(path, k)
		if err != nil {
			return "", originCreated, newFailure(100, "DATADIR",
				fmt.Sprintf("leitura de %s falhou: %v", path, err))
		}
		if v := k.normalize(adopted); k.valid(v) {
			return v, originRace, nil
		}
		// O arquivo já existia ou o vencedor gravou lixo.
		r.warnf("%s: %s existe com conteúdo inválido (%s) e será substituído",
			k.variable, path, describeInvalid(adopted))
		corrupt = true
	}

	origin := originRegenerated
	for attempt := 0; attempt < regenAttempts; attempt++ {
		created, err := r.sys.CreateExclusive(regen, identLine(value), filePerm)
		if err != nil {
			return "", originCreated, newFailure(k.writeCode, k.variable,
				fmt.Sprintf("gravação em %s falhou: %v", regen, err))
		}

		winner := value
		if !created {
			// Outro processo já registrou a regeneração ou registro anterior existe.
			recorded, err := r.readSettled(regen, k)
			if err != nil {
				return "", originCreated, newFailure(100, "DATADIR",
					fmt.Sprintf("leitura de %s falhou: %v", regen, err))
			}
			v := k.normalize(recorded)
			if !k.valid(v) {
				if err := r.sys.Remove(regen); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return "", originCreated, newFailure(k.writeCode, k.variable,
						fmt.Sprintf("remoção de %s falhou: %v", regen, err))
				}
				continue
			}
			winner = v
			origin = originRecord
		}

		// Vencedor e perdedores gravam exatamente o mesmo conteúdo.
		if err := r.sys.ReplaceFile(path, identLine(winner), filePerm); err != nil {
			return "", originCreated, newFailure(k.writeCode, k.variable,
				fmt.Sprintf("gravação em %s falhou: %v", path, err))
		}
		return winner, origin, nil
	}

	return "", originCreated, newFailure(k.writeCode, k.variable,
		fmt.Sprintf("não foi possível registrar a regeneração em %s após %d tentativas",
			regen, regenAttempts))
}

// reportPersisted emite o diagnóstico — e o aviso, quando uma identidade
// persistida foi descartada ou restaurada — coerente com persistGenerated.
func (r *resolver) reportPersisted(k identKind, subject, generated, adopted string, origin persistOrigin) {
	path := filepath.Join(r.dataDir, k.file)
	regen := filepath.Join(r.dataDir, "."+k.file+regenSuffix)
	switch origin {
	case originRecord:
		r.warnf("%s: %s foi RESTAURADO a partir do registro de regeneração %s (%s). "+
			"O registro pode vir de uma recuperação anterior; se a intenção era uma identidade nova, "+
			"apague %s (não o esvazie): a criação do zero descarta o registro",
			k.variable, path, regen, describeInvalid(adopted), path)
		r.logf("%s: file %s = %q (restaurado do registro de regeneração)", k.variable, path, adopted)
	case originRegenerated:
		r.warnf("%s: %s foi REGERADO com valor novo (%s); a identidade %s muda a partir de agora",
			k.variable, path, describeInvalid(adopted), subject)
		r.logf("%s: generated = %q", k.variable, generated)
	case originRace:
		r.logf("%s: file %s = %q (definido por outro processo)", k.variable, path, adopted)
	default:
		r.logf("%s: generated = %q", k.variable, generated)
	}
}

// env retorna a variável de ambiente com trim de whitespace. Usada para os
// CAMINHOS (DATADIR, MACHINE_ID_FILE), que não passam pelo pipeline de campo.
func (r *resolver) env(key string) string {
	return strings.TrimSpace(r.sys.Getenv(key))
}

// envField retorna a variável de ambiente já pelo mesmo pipeline aplicado às
// fontes de arquivo (SPEC §5: 1ª linha → trim → sanear). Antes, envs pulavam o
// firstLine e as duas fontes do mesmo campo tinham saneamentos diferentes.
func (r *resolver) envField(key string) string {
	return firstLine([]byte(r.sys.Getenv(key)))
}

// ----- DATADIR -----

// resolveDataDirPath resolve o CAMINHO do DATADIR (env ou padrão), normaliza
// com filepath.Clean e exige caminho absoluto. A existência é validada de forma
// preguiçosa por dataDirUsable/ensureDataDir.
//
// O caminho precisa ser absoluto porque a identidade não pode depender do
// diretório de trabalho: com "DATADIR=dados", o mesmo serviço iniciado de outro
// lugar (um WorkingDirectory diferente no unit do systemd, um chdir da
// aplicação) leria outro arquivo e viraria outro agente.
func hasControlBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func (r *resolver) resolveDataDirPath() *failure {
	dir := r.env("DATADIR")
	origin := "env"
	if dir == "" {
		dir, origin = DefaultDataDir, "padrão"
	}
	if hasControlBytes(dir) {
		return newFailure(100, "DATADIR",
			fmt.Sprintf("caminho %s contém caractere de controle", preview(dir)))
	}
	if !rootedPath(dir) {
		return newFailure(100, "DATADIR",
			fmt.Sprintf("%q é relativo ao diretório de trabalho; use um caminho absoluto", dir))
	}
	r.dataDir = filepath.Clean(dir)
	if runtime.GOOS == "windows" && filepath.VolumeName(r.dataDir) == "" {
		r.logf("DATADIR: %q sem letra de unidade; resolvido contra a unidade corrente do processo", r.dataDir)
	}
	r.logf("DATADIR: %s = %q", origin, r.dataDir)
	return nil
}

// rootedPath informa se o caminho está ancorado na raiz, ou seja, se NÃO varia
// com o diretório de trabalho do processo.
//
// filepath.IsAbs sozinho não serve: em Windows ele recusa "/data" — o próprio
// DefaultDataDir desta biblioteca — porque lá um caminho absoluto exige letra de
// unidade. Um caminho iniciado por separador é ancorado na raiz e não muda com o
// CWD, que é exatamente a garantia que este guarda precisa dar. Continuam
// recusados "dados", "./dados", "../dados" e, em Windows, o "C:dados" relativo
// à unidade.
func rootedPath(p string) bool {
	if filepath.IsAbs(p) {
		return true
	}
	return p != "" && (p[0] == '/' || p[0] == os.PathSeparator)
}

// ----- MACHINE_ID -----

// normalizeMachineID aplica o saneamento específico do machine-id:
// lowercase e remoção de hífens.
func normalizeMachineID(raw string) string {
	return strings.ToLower(strings.ReplaceAll(raw, "-", ""))
}

// machineIDFilePath decide qual arquivo de machine-id do SO consultar no nível
// 2, e informa se o caminho foi escolhido EXPLICITAMENTE pelo operador.
//
// A distinção importa no tratamento de erro: um /etc/machine-id ilegível é uma
// fonte do sistema que simplesmente não serviu, mas um $MACHINE_ID_FILE
// ilegível é uma instrução do operador que não pôde ser cumprida — e ele
// precisa saber disso em vez de receber outra identidade em silêncio.
func (r *resolver) machineIDFilePath() (path string, explicit bool, f *failure) {
	midFile := r.env("MACHINE_ID_FILE")
	if midFile == "" {
		return DefaultMachineIDFile, false, nil
	}
	if hasControlBytes(midFile) {
		return "", true, newFailure(100, "MACHINE_ID_FILE",
			fmt.Sprintf("caminho %s contém caractere de controle", preview(midFile)))
	}
	if !rootedPath(midFile) {
		return "", true, newFailure(100, "MACHINE_ID_FILE",
			fmt.Sprintf("%q é relativo ao diretório de trabalho; use um caminho absoluto", midFile))
	}
	midFile = filepath.Clean(midFile)
	info, err := r.sys.Stat(midFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// SPEC §7.2: env apontando para arquivo inexistente troca para o
			// padrão do sistema. Apenas "inexistente" — não "qualquer erro".
			r.logf("MACHINE_ID_FILE: %q inexistente, usando %s", midFile, DefaultMachineIDFile)
			return DefaultMachineIDFile, false, nil
		}
		return "", true, newFailure(100, "MACHINE_ID_FILE",
			fmt.Sprintf("%q inacessível: %v", midFile, err))
	}
	if !info.Mode().IsRegular() || info.Size() > maxIdentFileSize {
		return "", true, newFailure(100, "MACHINE_ID_FILE",
			fmt.Sprintf("%q não é um arquivo comum utilizável (tipo %s, %d bytes; limite %d)",
				midFile, info.Mode().Type(), info.Size(), maxIdentFileSize))
	}
	return midFile, true, nil
}

// resolveMachineID implementa a cadeia de 4 níveis do MACHINE_ID:
// env → $MACHINE_ID_FILE → $DATADIR/machine_id → geração.
func (r *resolver) resolveMachineID() (string, *failure) {
	// Nível 1: env explícita. Presente e inválida aborta (102).
	if raw := r.envField("MACHINE_ID"); raw != "" {
		v := normalizeMachineID(raw)
		if !validMachineID(v) {
			return "", newFailure(102, "MACHINE_ID",
				fmt.Sprintf("%s não casa com %s", preview(raw), patMachineID))
		}
		r.logf("MACHINE_ID: env = %q", v)
		return v, nil
	}

	// Nível 2: arquivo de machine-id do SO. Conteúdo inválido aqui apenas CAI.
	midFile, explicit, f := r.machineIDFilePath()
	if f != nil {
		return "", f
	}
	raw, err := r.readFile(midFile)
	switch {
	case err != nil && explicit:
		return "", newFailure(100, "MACHINE_ID_FILE",
			fmt.Sprintf("leitura de %q falhou: %v", midFile, err))
	case err != nil:
		r.logf("MACHINE_ID: %s ilegível (%v), caindo para o próximo nível", midFile, err)
	case raw != "":
		if v := normalizeMachineID(raw); validMachineID(v) {
			r.logf("MACHINE_ID: file %s = %q", midFile, v)
			return v, nil
		}
		r.logf("MACHINE_ID: conteúdo de %s inválido, caindo para o próximo nível", midFile)
	}

	// Nível 3: arquivo auto-gerido em $DATADIR. Vazio/inválido CAI (regenera).
	stored, f := r.readDataFile(fileMachineID)
	if f != nil {
		return "", f
	}
	corrupt := false
	if stored != "" {
		if v := normalizeMachineID(stored); validMachineID(v) {
			r.logf("MACHINE_ID: file %s = %q", filepath.Join(r.dataDir, fileMachineID), v)
			return v, nil
		}
		corrupt = true
		r.warnf("MACHINE_ID: %s tinha conteúdo inválido (%s) e será substituído "+
			"(o aviso seguinte diz se foi regenerado ou restaurado de um registro)",
			filepath.Join(r.dataDir, fileMachineID), describeInvalid(stored))
	}

	// Nível 4: gerar a partir de um UUIDv7 (hífens removidos => 32 hex).
	uuid, err := r.sys.GenerateUUIDv7()
	if err != nil {
		return "", newFailure(114, "MACHINE_ID",
			fmt.Sprintf("geração de UUIDv7 base falhou: %v", err))
	}
	generated := normalizeMachineID(uuid)
	if !validMachineID(generated) {
		return "", newFailure(114, "MACHINE_ID",
			fmt.Sprintf("valor gerado %q não casa com %s", generated, patMachineID))
	}

	adopted, origin, f := r.persistGenerated(kindMachineID, generated, corrupt)
	if f != nil {
		return "", f
	}
	r.reportPersisted(kindMachineID, "desta máquina", generated, adopted, origin)
	return adopted, nil
}

// ----- AGENT_NAME -----

// resolveAgentName implementa a cadeia: env → $DATADIR/agent_name → argv[0].
func (r *resolver) resolveAgentName() (string, *failure) {
	// Nível 1: env. Presente e inválida aborta (104). Nunca grava arquivo.
	if raw := strings.ToLower(r.envField("AGENT_NAME")); raw != "" {
		if !validAgentName(raw) {
			return "", newFailure(104, "AGENT_NAME",
				fmt.Sprintf("%s não casa com %s (máx. %d caracteres)", preview(raw), patAgentName, maxFieldLen))
		}
		r.logf("AGENT_NAME: env = %q", raw)
		return raw, nil
	}

	// Nível 2: arquivo. Presente e inválido aborta (104).
	v, f := r.readDataFile(fileAgentName)
	if f != nil {
		return "", f
	}
	if v != "" {
		v = strings.ToLower(v)
		if !validAgentName(v) {
			return "", newFailure(104, "AGENT_NAME",
				fmt.Sprintf("conteúdo de %s (%s) não casa com %s (máx. %d caracteres)",
					filepath.Join(r.dataDir, fileAgentName), describeInvalid(v), patAgentName, maxFieldLen))
		}
		r.logf("AGENT_NAME: file %s = %q", filepath.Join(r.dataDir, fileAgentName), v)
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
	// filepath.Base devolve o separador da PLATAFORMA para uma raiz: em Windows
	// filepath.Base(`C:\`) é `\`, que escaparia de uma guarda fixa em "/".
	if base == "" || base == "." || base == "/" || base == string(os.PathSeparator) {
		return "", newFailure(103, "AGENT_NAME",
			"env vazia, arquivo ausente e argv[0] ficou vazio")
	}
	if !validAgentName(base) {
		return "", newFailure(104, "AGENT_NAME",
			fmt.Sprintf("%q (de argv[0]) não casa com %s (máx. %d caracteres)",
				base, patAgentName, maxFieldLen))
	}
	r.logf("AGENT_NAME: fallback argv[0] = %q", base)
	return base, nil
}

// ----- AGENT_UUID -----

// resolveAgentUUID implementa a cadeia: env → $DATADIR/agent_uuid → geração.
func (r *resolver) resolveAgentUUID() (string, *failure) {
	// Nível 1: env. Presente e inválida aborta (107). Nunca grava arquivo.
	if raw := strings.ToLower(r.envField("AGENT_UUID")); raw != "" {
		if !validAgentUUID(raw) {
			return "", newFailure(107, "AGENT_UUID",
				fmt.Sprintf("%s não é um UUIDv7 canônico", preview(raw)))
		}
		r.logf("AGENT_UUID: env = %q", raw)
		return raw, nil
	}

	// Nível 2: arquivo auto-gerido. Vazio/inválido CAI (regenera).
	stored, f := r.readDataFile(fileAgentUUID)
	if f != nil {
		return "", f
	}
	corrupt := false
	if stored != "" {
		if v := strings.ToLower(stored); validAgentUUID(v) {
			r.logf("AGENT_UUID: file %s = %q", filepath.Join(r.dataDir, fileAgentUUID), v)
			return v, nil
		}
		corrupt = true
		r.warnf("AGENT_UUID: %s tinha conteúdo inválido (%s) e será substituído "+
			"(o aviso seguinte diz se foi regenerado ou restaurado de um registro)",
			filepath.Join(r.dataDir, fileAgentUUID), describeInvalid(stored))
	}

	// Nível 3: gerar UUIDv7 e persistir.
	uuid, err := r.sys.GenerateUUIDv7()
	if err != nil {
		return "", newFailure(105, "AGENT_UUID",
			fmt.Sprintf("geração de UUIDv7 falhou: %v", err))
	}
	generated := strings.ToLower(firstLine([]byte(uuid)))
	if !validAgentUUID(generated) {
		return "", newFailure(107, "AGENT_UUID",
			fmt.Sprintf("valor gerado %q não é um UUIDv7 canônico", generated))
	}

	adopted, origin, f := r.persistGenerated(kindAgentUUID, generated, corrupt)
	if f != nil {
		return "", f
	}
	r.reportPersisted(kindAgentUUID, "deste agente", generated, adopted, origin)
	return adopted, nil
}

// ----- HOSTNAME -----

// resolveHostname implementa a cadeia: env → os.Hostname().
func (r *resolver) resolveHostname() (string, *failure) {
	raw := r.envField("HOSTNAME")
	origin := "env"
	if raw == "" {
		v, err := r.sys.Hostname()
		if err != nil {
			return "", newFailure(108, "HOSTNAME",
				fmt.Sprintf("os.Hostname() falhou: %v", err))
		}
		raw = firstLine([]byte(v))
		origin = "os.Hostname"
	}
	raw = strings.ToLower(raw)
	if !validHostname(raw) {
		return "", newFailure(109, "HOSTNAME",
			fmt.Sprintf("%s não casa com %s nem com as regras de rótulo da RFC 1123", preview(raw), patHostname))
	}
	r.logf("HOSTNAME: %s = %q", origin, raw)
	return raw, nil
}

// ----- WORKSPACE -----

// resolveWorkspace implementa a cadeia: env → $DATADIR/workspace → "default".
func (r *resolver) resolveWorkspace() (string, *failure) {
	// Nível 1: env. Presente e inválida aborta (111).
	if raw := strings.ToLower(r.envField("WORKSPACE")); raw != "" {
		if !validWorkspace(raw) {
			return "", newFailure(111, "WORKSPACE",
				fmt.Sprintf("%s não casa com %s (máx. %d caracteres)", preview(raw), patWorkspace, maxFieldLen))
		}
		r.logf("WORKSPACE: env = %q", raw)
		return raw, nil
	}

	// Nível 2: arquivo. Presente e inválido aborta (111).
	v, f := r.readDataFile(fileWorkspace)
	if f != nil {
		return "", f
	}
	if v != "" {
		v = strings.ToLower(v)
		if !validWorkspace(v) {
			return "", newFailure(111, "WORKSPACE",
				fmt.Sprintf("conteúdo de %s (%s) não casa com %s (máx. %d caracteres)",
					filepath.Join(r.dataDir, fileWorkspace), describeInvalid(v), patWorkspace, maxFieldLen))
		}
		r.logf("WORKSPACE: file %s = %q", filepath.Join(r.dataDir, fileWorkspace), v)
		return v, nil
	}

	// Nível 3: fallback determinístico (não grava arquivo).
	r.logf("WORKSPACE: fallback = %q", DefaultWorkspace)
	return DefaultWorkspace, nil
}
