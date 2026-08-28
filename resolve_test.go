package loghubident

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSystem é uma implementação em memória de system, usada nos testes.
type fakeSystem struct {
	env      map[string]string
	files    map[string]string // caminho -> conteúdo
	dirs     map[string]bool   // caminhos que existem e são diretórios
	hostname string
	hostErr  error
	args     []string

	uuid    string // UUID devolvido por GenerateUUIDv7
	uuidErr error  // erro devolvido por GenerateUUIDv7

	readErr   map[string]error       // caminho -> erro forçado na leitura
	writeErr  map[string]error       // caminho -> erro forçado na gravação
	written   map[string]string      // caminho -> conteúdo gravado
	perms     map[string]os.FileMode // caminho -> permissão recebida na gravação
	uuidCalls int

	// onCreateRefused é chamado quando CreateExclusive recusa a criação porque
	// o arquivo já existe. Permite reproduzir a janela da corrida entre
	// processos: o vencedor materializa o arquivo entre a nossa leitura e a
	// nossa gravação.
	onCreateRefused func(path string)
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{
		env:      map[string]string{},
		files:    map[string]string{},
		dirs:     map[string]bool{},
		args:     []string{"/usr/local/bin/myservice"},
		hostname: "node01",
		uuid:     "019e99e3-42f0-7882-9719-2305ff84949c",
		readErr:  map[string]error{},
		writeErr: map[string]error{},
		written:  map[string]string{},
		perms:    map[string]os.FileMode{},
	}
}

func (f *fakeSystem) Getenv(key string) string { return f.env[key] }

func (f *fakeSystem) Stat(path string) (os.FileInfo, error) {
	if f.dirs[path] {
		return fakeInfo{name: filepath.Base(path), dir: true}, nil
	}
	if _, ok := f.files[path]; ok {
		return fakeInfo{name: filepath.Base(path)}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
}

func (f *fakeSystem) ReadFile(path string) ([]byte, error) {
	if err := f.readErr[path]; err != nil {
		return nil, err
	}
	if content, ok := f.files[path]; ok {
		return []byte(content), nil
	}
	return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
}

// CreateExclusive só grava se o caminho ainda não existir, como o O_EXCL real.
func (f *fakeSystem) CreateExclusive(path string, data []byte, perm os.FileMode) (bool, error) {
	if err := f.writeErr[path]; err != nil {
		return false, err
	}
	if _, ok := f.files[path]; ok {
		if f.onCreateRefused != nil {
			f.onCreateRefused(path)
		}
		return false, nil
	}
	f.store(path, data, perm)
	return true, nil
}

func (f *fakeSystem) ReplaceFile(path string, data []byte, perm os.FileMode) error {
	if err := f.writeErr[path]; err != nil {
		return err
	}
	f.store(path, data, perm)
	return nil
}

// store registra conteúdo E permissão, para que os testes possam conferir que
// a lib pede sempre 0644 (o umask não existe no fake).
func (f *fakeSystem) store(path string, data []byte, perm os.FileMode) {
	f.files[path] = string(data)
	f.written[path] = string(data)
	f.perms[path] = perm
}

func (f *fakeSystem) Remove(path string) error {
	if _, ok := f.files[path]; !ok {
		return &fs.PathError{Op: "remove", Path: path, Err: fs.ErrNotExist}
	}
	delete(f.files, path)
	return nil
}

func (f *fakeSystem) Hostname() (string, error) {
	if f.hostErr != nil {
		return "", f.hostErr
	}
	return f.hostname, nil
}

func (f *fakeSystem) Args() []string { return f.args }

func (f *fakeSystem) GenerateUUIDv7() (string, error) {
	f.uuidCalls++
	if f.uuidErr != nil {
		return "", f.uuidErr
	}
	return f.uuid, nil
}

// fakeInfo é um os.FileInfo mínimo para Stat.
type fakeInfo struct {
	name string
	dir  bool
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return 0 }
func (i fakeInfo) Mode() os.FileMode  { return 0 }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return i.dir }
func (i fakeInfo) Sys() any           { return nil }

// withDataDir marca /data como diretório existente.
func (f *fakeSystem) withDataDir() *fakeSystem {
	f.dirs["/data"] = true
	return f
}

// ----- Caminho feliz: tudo vindo de env (filesystem read-only) -----

func TestResolve_AllFromEnv_NoDataDirNeeded(t *testing.T) {
	sys := newFakeSystem() // note: NÃO marcamos /data como diretório
	sys.env = map[string]string{
		"MACHINE_ID": "ABCDEF0123456789ABCDEF0123456789",
		"AGENT_NAME": "My-Service",
		"AGENT_UUID": "019E99E3-42F0-7882-9719-2305FF84949C",
		"HOSTNAME":   "Node01",
		"WORKSPACE":  "Production",
	}

	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.machineID != "abcdef0123456789abcdef0123456789" {
		t.Errorf("machineID = %q", id.machineID)
	}
	if id.agentName != "my-service" {
		t.Errorf("agentName = %q", id.agentName)
	}
	if id.agentUUID != "019e99e3-42f0-7882-9719-2305ff84949c" {
		t.Errorf("agentUUID = %q", id.agentUUID)
	}
	if id.hostname != "node01" {
		t.Errorf("hostname = %q", id.hostname)
	}
	if id.workspace != "production" {
		t.Errorf("workspace = %q", id.workspace)
	}
	if len(sys.written) != 0 {
		t.Errorf("não deveria gravar nada, gravou: %v", sys.written)
	}
}

// ----- DATADIR -----

func TestResolve_DataDir_LazyValidation_FatalOnlyWhenNeeded(t *testing.T) {
	// AGENT_UUID ausente força a geração e a gravação em $DATADIR, que não
	// existe -> código 100.
	sys := newFakeSystem()
	sys.env = map[string]string{
		"MACHINE_ID": "abcdef0123456789abcdef0123456789",
		"AGENT_NAME": "svc",
		"HOSTNAME":   "node01",
		"WORKSPACE":  "prod",
	}
	_, f := resolve(sys)
	if f == nil || f.code != 100 {
		t.Fatalf("esperava código 100, obtive %+v", f)
	}
}

func TestResolve_DataDir_ReadIOError_Aborts100(t *testing.T) {
	// Um arquivo auto-gerido em $DATADIR existe mas a leitura falha por um erro
	// de I/O real (não "ausente"). Isso NÃO deve ser mascarado como fonte vazia:
	// deve abortar com 100 para não esconder um problema do filesystem.
	sys := newFakeSystem().withDataDir()
	sys.env = map[string]string{
		"MACHINE_ID": "abcdef0123456789abcdef0123456789",
		"AGENT_NAME": "svc",
		"HOSTNAME":   "node01",
		"WORKSPACE":  "prod",
	}
	// AGENT_UUID ausente -> lê /data/agent_uuid, que falha com EACCES.
	sys.readErr["/data/agent_uuid"] = &fs.PathError{
		Op: "open", Path: "/data/agent_uuid", Err: fs.ErrPermission,
	}
	_, f := resolve(sys)
	if f == nil || f.code != 100 {
		t.Fatalf("esperava código 100 por erro de I/O, obtive %+v", f)
	}
	if sys.uuidCalls != 0 {
		t.Errorf("não deveria gerar UUID após erro de I/O, gerou %d vez(es)", sys.uuidCalls)
	}
}

func TestResolve_DataDir_CustomPath(t *testing.T) {
	sys := newFakeSystem()
	sys.dirs["/var/lib/app"] = true
	sys.env["DATADIR"] = "/var/lib/app"
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	// AGENT_UUID ausente -> gera e grava em /var/lib/app/agent_uuid
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.dataDir != "/var/lib/app" {
		t.Errorf("dataDir = %q", id.dataDir)
	}
	if _, ok := sys.written["/var/lib/app/agent_uuid"]; !ok {
		t.Errorf("agent_uuid não foi gravado em /var/lib/app")
	}
}

// ----- MACHINE_ID -----

func TestResolve_MachineID_EnvInvalid_Aborts102(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "not-hex"
	_, f := resolve(sys)
	if f == nil || f.code != 102 {
		t.Fatalf("esperava 102, obtive %+v", f)
	}
}

func TestResolve_MachineID_FromMachineIDFile(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.files["/etc/machine-id"] = "abcdef0123456789abcdef0123456789\n"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.machineID != "abcdef0123456789abcdef0123456789" {
		t.Errorf("machineID = %q", id.machineID)
	}
}

func TestResolve_MachineID_CustomFileMissing_FallsBackToDefault(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID_FILE"] = "/custom/missing"
	sys.files["/etc/machine-id"] = "abcdef0123456789abcdef0123456789\n"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.machineID != "abcdef0123456789abcdef0123456789" {
		t.Errorf("machineID = %q", id.machineID)
	}
}

func TestResolve_MachineID_InvalidFileFallsThroughToGenerate(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.files["/etc/machine-id"] = "garbage!!!\n" // inválido -> cai
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	// gerado a partir do UUID padrão do fake (sem hífens)
	if id.machineID != "019e99e342f0788297192305ff84949c" {
		t.Errorf("machineID = %q", id.machineID)
	}
	if _, ok := sys.written["/data/machine_id"]; !ok {
		t.Errorf("machine_id gerado não foi persistido")
	}
}

func TestResolve_MachineID_WriteFailure113(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.writeErr["/data/machine_id"] = errors.New("read-only fs")
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 113 {
		t.Fatalf("esperava 113, obtive %+v", f)
	}
}

func TestResolve_MachineID_DataDirFileWins(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	// sem /etc/machine-id; arquivo em $DATADIR válido
	sys.files["/data/machine_id"] = "11112222333344445555666677778888\n"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.machineID != "11112222333344445555666677778888" {
		t.Errorf("machineID = %q", id.machineID)
	}
}

// ----- AGENT_NAME -----

func TestResolve_AgentName_FromArgv0StripExe(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.args = []string{"/opt/bin/MyApp.EXE"}
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentName != "myapp" {
		t.Errorf("agentName = %q (esperava myapp)", id.agentName)
	}
	if _, ok := sys.written["/data/agent_name"]; ok {
		t.Errorf("agent_name de argv[0] não deveria ser gravado")
	}
}

func TestResolve_AgentName_FromValidFile(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.files["/data/agent_name"] = "Edge-Collector\n" // lowercase aplicado
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentName != "edge-collector" {
		t.Errorf("agentName = %q (esperava edge-collector)", id.agentName)
	}
}

func TestResolve_AgentName_FileInvalidAborts104(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.files["/data/agent_name"] = "Invalid Name!\n"
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 104 {
		t.Fatalf("esperava 104, obtive %+v", f)
	}
}

func TestResolve_AgentName_Argv0EmptyAborts103(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.args = []string{""}
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 103 {
		t.Fatalf("esperava 103, obtive %+v", f)
	}
}

// ----- AGENT_UUID -----

func TestResolve_AgentUUID_GeneratesAndPersists(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentUUID != "019e99e3-42f0-7882-9719-2305ff84949c" {
		t.Errorf("agentUUID = %q", id.agentUUID)
	}
	if got := sys.written["/data/agent_uuid"]; !strings.HasPrefix(got, id.agentUUID) {
		t.Errorf("agent_uuid persistido = %q", got)
	}
}

func TestResolve_AgentUUID_FromValidFile(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.files["/data/agent_uuid"] = "01900000-0000-7000-8000-000000000abc\n"
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentUUID != "01900000-0000-7000-8000-000000000abc" {
		t.Errorf("agentUUID = %q", id.agentUUID)
	}
	if sys.uuidCalls != 0 {
		t.Errorf("não deveria gerar UUID com arquivo válido, gerou %d", sys.uuidCalls)
	}
}

func TestResolve_AgentUUID_CorruptFileRegenerates(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.files["/data/agent_uuid"] = "xxxx-not-a-uuid\n"
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentUUID != "019e99e3-42f0-7882-9719-2305ff84949c" {
		t.Errorf("agentUUID = %q (esperava regeneração)", id.agentUUID)
	}
}

func TestResolve_AgentUUID_EnvInvalidAborts107(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "deadbeef" // inválido
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 107 {
		t.Fatalf("esperava 107, obtive %+v", f)
	}
}

func TestResolve_AgentUUID_GenFailure105(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.uuidErr = errors.New("entropia indisponível")
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 105 {
		t.Fatalf("esperava 105, obtive %+v", f)
	}
}

func TestResolve_AgentUUID_WriteFailure106(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.writeErr["/data/agent_uuid"] = errors.New("read-only fs")
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 106 {
		t.Fatalf("esperava 106, obtive %+v", f)
	}
}

// ----- HOSTNAME -----

func TestResolve_Hostname_OsError108(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.hostErr = errors.New("sem hostname")
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 108 {
		t.Fatalf("esperava 108, obtive %+v", f)
	}
}

func TestResolve_Hostname_Invalid109(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.hostname = "host_with_underscore"
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 109 {
		t.Fatalf("esperava 109, obtive %+v", f)
	}
}

// ----- WORKSPACE -----

func TestResolve_Workspace_DefaultFallback(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	// sem WORKSPACE e sem arquivo -> "default"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.workspace != DefaultWorkspace {
		t.Errorf("workspace = %q (esperava %q)", id.workspace, DefaultWorkspace)
	}
}

func TestResolve_Workspace_Invalid111(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "Bad Workspace!"
	_, f := resolve(sys)
	if f == nil || f.code != 111 {
		t.Fatalf("esperava 111, obtive %+v", f)
	}
}

func TestResolve_Workspace_FromFile(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.files["/data/workspace"] = "Staging\n" // lowercase aplicado na resolução
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.workspace != "staging" {
		t.Errorf("workspace = %q (esperava staging)", id.workspace)
	}
}

func TestResolve_Workspace_FileInvalidAborts111(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.files["/data/workspace"] = "bad workspace!\n"
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	_, f := resolve(sys)
	if f == nil || f.code != 111 {
		t.Fatalf("esperava 111, obtive %+v", f)
	}
}

// ----- Diagnóstico (debug) -----

func TestResolve_DebugLines_ReportOrigins(t *testing.T) {
	sys := newFakeSystem() // tudo via env -> nenhuma trilha toca o disco
	sys.env = map[string]string{
		"MACHINE_ID": "abcdef0123456789abcdef0123456789",
		"AGENT_NAME": "svc",
		"AGENT_UUID": "019e99e3-42f0-7882-9719-2305ff84949c",
		"HOSTNAME":   "node01",
		"WORKSPACE":  "prod",
	}
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	joined := strings.Join(id.debug, "\n")
	for _, want := range []string{
		"MACHINE_ID: env", "AGENT_NAME: env", "AGENT_UUID: env",
		"HOSTNAME: env", "WORKSPACE: env",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("linha de debug ausente: %q\nobtido:\n%s", want, joined)
		}
	}
}

// ----- Getters / apply -----

func TestApplyAndGetters(t *testing.T) {
	apply(&identity{
		dataDir:   "/data",
		machineID: "abcdef0123456789abcdef0123456789",
		agentName: "svc",
		agentUUID: "019e99e3-42f0-7882-9719-2305ff84949c",
		hostname:  "node01",
		workspace: "prod",
	})
	if DataDir() != "/data" || MachineID() == "" || AgentName() != "svc" ||
		AgentUUID() == "" || Hostname() != "node01" || Workspace() != "prod" {
		t.Errorf("getters não refletiram apply()")
	}
}

// ----- BUG-18: falha na geração do MACHINE_ID tem código próprio (114) -----

// A SPEC §13 reserva o 105 exclusivamente para AGENT_UUID. Emitir 105 numa
// falha de MACHINE_ID levaria o operador a diagnosticar o campo errado.
func TestResolve_MachineID_GenFailure114(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.uuidErr = errors.New("entropia indisponível")
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil {
		t.Fatal("esperava falha, obtive sucesso")
	}
	if f.code != 114 {
		t.Fatalf("código de falha = %d (esperava 114)", f.code)
	}
	if f.variable != "MACHINE_ID" {
		t.Errorf("variável reportada = %q", f.variable)
	}
}

// O 105 continua sendo do AGENT_UUID: com o MACHINE_ID vindo da env, a única
// geração de UUID que resta é a do agent_uuid.
func TestResolve_AgentUUID_GenFailureStays105(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.uuidErr = errors.New("entropia indisponível")
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil {
		t.Fatal("esperava falha, obtive sucesso")
	}
	if f.code != 105 || f.variable != "AGENT_UUID" {
		t.Fatalf("falha = %d/%s (esperava 105/AGENT_UUID)", f.code, f.variable)
	}
}

// ----- BUG-08: os.IsNotExist não desembrulha erros -----

// A interface system existe para ser substituída, e embrulhar erro com %w é
// idioma padrão em Go. Com os.IsNotExist — que só entende *PathError direto —
// um system alternativo converteria "arquivo ausente" em aborto com 100.
func TestResolve_WrappedNotExistIsTreatedAsAbsent(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	// AGENT_NAME e WORKSPACE ausentes: as leituras em $DATADIR devolvem um
	// ErrNotExist EMBRULHADO, como faria qualquer system que acrescente contexto.
	for _, name := range []string{"/data/agent_name", "/data/workspace"} {
		sys.readErr[name] = fmt.Errorf("camada de storage: %w",
			&fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist})
	}

	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentName != "myservice" {
		t.Errorf("agentName = %q (esperava o fallback de argv[0])", id.agentName)
	}
	if id.workspace != DefaultWorkspace {
		t.Errorf("workspace = %q (esperava o fallback %q)", id.workspace, DefaultWorkspace)
	}
}

// Um erro de I/O legítimo, mesmo embrulhado, continua abortando com 100.
func TestResolve_WrappedIOErrorStillAborts(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	sys.readErr["/data/agent_name"] = fmt.Errorf("camada de storage: %w",
		&fs.PathError{Op: "open", Path: "/data/agent_name", Err: fs.ErrPermission})

	_, f := resolve(sys)
	if f == nil || f.code != 100 {
		t.Fatalf("esperava 100 por erro de I/O, obtive %+v", f)
	}
}

// ----- BUG-12: argv[0] vazio no separador do Windows -----

// filepath.Base devolve o separador da PLATAFORMA para uma raiz. Uma guarda
// fixa em "/" deixava o caso escapar e reprovar na validação, trocando o 103
// documentado por um 104.
func TestResolve_AgentName_RootArgv0Gives103(t *testing.T) {
	for _, argv0 := range []string{"", "/", string(os.PathSeparator)} {
		sys := newFakeSystem().withDataDir()
		sys.args = []string{argv0}
		sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
		sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
		sys.env["WORKSPACE"] = "prod"

		_, f := resolve(sys)
		if f == nil || f.code != 103 {
			t.Errorf("argv[0]=%q: esperava 103, obtive %+v", argv0, f)
		}
	}
}

// ----- BUG-05: a permissão pedida é sempre 0644 -----

func TestResolve_WritesUse0644(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["WORKSPACE"] = "prod"
	if _, f := resolve(sys); f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	for _, path := range []string{"/data/machine_id", "/data/agent_uuid"} {
		if got, ok := sys.perms[path]; !ok {
			t.Errorf("%s não foi gravado", path)
		} else if got != filePerm {
			t.Errorf("%s gravado com %04o (esperava %04o)", path, got, filePerm)
		}
	}
}

// ----- BUG-04: quem perde a corrida adota o valor do vencedor -----

// CreateExclusive devolvendo (false, nil) significa que outro processo criou o
// arquivo primeiro. O valor DELE é que vale — do contrário processos irmãos
// seguiriam com identidades diferentes e sobrescreveriam o disco.
func TestResolve_AgentUUID_LosesRaceAdoptsWinner(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	// O arquivo já existe: o CreateExclusive do fake devolve (false, nil).
	winner := "0190aaaa-bbbb-7ccc-8ddd-eeeeffff0000"
	sys.files["/data/agent_uuid"] = winner + "\n"
	// Mas a leitura do NÍVEL 2 não deve encontrá-lo: simulamos a corrida real,
	// em que o vencedor gravou entre a nossa leitura e a nossa gravação.
	sys.readErr["/data/agent_uuid"] = &fs.PathError{
		Op: "open", Path: "/data/agent_uuid", Err: fs.ErrNotExist,
	}

	id, f := resolveRacing(sys, "/data/agent_uuid")
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentUUID != winner {
		t.Errorf("agentUUID = %q (esperava adotar o vencedor %q)", id.agentUUID, winner)
	}
	if got := sys.files["/data/agent_uuid"]; got != winner+"\n" {
		t.Errorf("o arquivo do vencedor foi sobrescrito: %q", got)
	}
}

// resolveRacing executa resolve liberando a leitura de path assim que a
// primeira gravação exclusiva for recusada — reproduzindo a janela em que o
// processo vencedor materializa o arquivo.
func resolveRacing(sys *fakeSystem, path string) (*identity, *failure) {
	sys.onCreateRefused = func(p string) {
		if p == path {
			delete(sys.readErr, p)
		}
	}
	return resolve(sys)
}

// ----- Validadores (substituem as regexes; ver PERF-01, BUG-06, BUG-11) -----

func TestValidators(t *testing.T) {
	t.Run("machineID", func(t *testing.T) {
		checkValidator(t, validMachineID,
			[]string{"abcdef0123456789abcdef0123456789", "00000000000000000000000000000000"},
			[]string{"", "abcdef", "ABCDEF0123456789ABCDEF0123456789",
				"abcdef0123456789abcdef012345678g", "abcdef0123456789abcdef01234567890"})
	})

	t.Run("agentUUID", func(t *testing.T) {
		checkValidator(t, validAgentUUID,
			[]string{"019e99e3-42f0-7882-9719-2305ff84949c", "00000000-0000-7000-8000-000000000000"},
			[]string{"", "deadbeef",
				"019e99e3-42f0-4882-9719-2305ff84949c", // versão 4
				"019e99e3-42f0-7882-c719-2305ff84949c", // variante inválida
				"019e99e342f0788297192305ff84949c",     // sem hífens
				"019E99E3-42F0-7882-9719-2305FF84949C", // maiúsculas
				"019e99e3_42f0-7882-9719-2305ff84949c"})
	})

	t.Run("agentName", func(t *testing.T) {
		checkValidator(t, validAgentName,
			[]string{"svc", "loghub-agent.v2_br", "a", strings.Repeat("a", maxFieldLen)},
			[]string{"", ".", "..", "Svc", "com espaço", "com/barra",
				strings.Repeat("a", maxFieldLen+1)})
	})

	t.Run("workspace", func(t *testing.T) {
		checkValidator(t, validWorkspace,
			[]string{"prod", "tenant.sub-1", strings.Repeat("a", maxFieldLen)},
			[]string{"", ".", "..", "com_underscore", strings.Repeat("a", maxFieldLen+1)})
	})

	t.Run("hostname", func(t *testing.T) {
		checkValidator(t, validHostname,
			[]string{"node01", "a", "debv.tmsoft.com.br", "web-1.svc.cluster.local"},
			[]string{"", "-", ".", "...", "-host-", "host-", "node01.",
				"com_underscore", strings.Repeat("h", maxHostnameLen+1),
				strings.Repeat("h", maxLabelLen+1) + ".com"})
	})
}

func checkValidator(t *testing.T, valid func(string) bool, good, bad []string) {
	t.Helper()
	for _, s := range good {
		if !valid(s) {
			t.Errorf("%q deveria ser aceito", s)
		}
	}
	for _, s := range bad {
		if valid(s) {
			t.Errorf("%q deveria ser recusado", s)
		}
	}
}

// ----- BUG-09 / PERF-03: saneamento da primeira linha -----

func TestFirstLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"svc", "svc"},
		{"svc\n", "svc"},
		{"svc\r\n", "svc"},
		{"svc\nsegunda", "svc"},
		{"  svc  \n", "svc"},
		{"\ufeffsvc\n", "svc"},          // BOM UTF-8 de editor do Windows
		{"svc\x00", "svc"},              // resíduo de gravação truncada
		{"\x00svc\x00\n", "svc"},        // idem, nas duas bordas
		{"\ufeff\tsvc \r\nlixo", "svc"}, // BOM + tab + segunda linha
		{"", ""},                        // fonte vazia
		{"\n", ""},                      // só a quebra
		{"\x00", ""},                    // só o byte nulo
	} {
		if got := firstLine([]byte(tc.in)); got != tc.want {
			t.Errorf("firstLine(%q) = %q (esperava %q)", tc.in, got, tc.want)
		}
	}
}

// ----- IMP-11: o conteúdo gravado tem exatamente uma quebra de linha -----

func TestIdentLine(t *testing.T) {
	for _, in := range []string{"svc", "svc\n", "svc\r\n", "svc\n\n"} {
		if got := string(identLine(in)); got != "svc\n" {
			t.Errorf("identLine(%q) = %q (esperava %q)", in, got, "svc\n")
		}
	}
}

// ----- BUG-10: DATADIR precisa estar ancorado na raiz -----

// O guarda recusa caminhos relativos ao CWD, mas não pode recusar o próprio
// DefaultDataDir: em Windows, filepath.IsAbs("/data") é false, porque lá um
// caminho absoluto exige letra de unidade.
func TestRootedPath(t *testing.T) {
	good := []string{DefaultDataDir, "/data", "/var/lib/app", string(os.PathSeparator) + "data"}
	bad := []string{"", "dados", "./dados", "../dados", "a/b"}
	for _, p := range good {
		if !rootedPath(p) {
			t.Errorf("%q deveria ser aceito", p)
		}
	}
	for _, p := range bad {
		if rootedPath(p) {
			t.Errorf("%q deveria ser recusado", p)
		}
	}
}

// O DATADIR padrão nunca pode derrubar a resolução por conta do guarda — em
// nenhuma plataforma.
func TestResolve_DefaultDataDirIsAccepted(t *testing.T) {
	sys := newFakeSystem() // /data NÃO existe: só o caminho é validado aqui
	sys.env = map[string]string{
		"MACHINE_ID": "abcdef0123456789abcdef0123456789",
		"AGENT_NAME": "svc",
		"AGENT_UUID": "019e99e3-42f0-7882-9719-2305ff84949c",
		"HOSTNAME":   "node01",
		"WORKSPACE":  "prod",
	}
	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("o DATADIR padrão foi recusado: %+v", f)
	}
	if id.dataDir != filepath.Clean(DefaultDataDir) {
		t.Errorf("dataDir = %q (esperava %q)", id.dataDir, filepath.Clean(DefaultDataDir))
	}
}

func TestResolve_RelativeDataDirAborts100(t *testing.T) {
	sys := newFakeSystem()
	sys.env["DATADIR"] = "dados"
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["AGENT_UUID"] = "019e99e3-42f0-7882-9719-2305ff84949c"
	sys.env["WORKSPACE"] = "prod"
	_, f := resolve(sys)
	if f == nil || f.code != 100 || f.variable != "DATADIR" {
		t.Fatalf("esperava 100/DATADIR, obtive %+v", f)
	}
}

// ----- BUG-04: convergência na REGENERAÇÃO de arquivo corrompido -----

// Quando o arquivo auto-gerido existe com conteúdo inválido, o nome já está
// ocupado e a criação exclusiva não arbitra nada. A disputa passa por um
// registro à parte: quem já o encontrar criado adota o valor DELE, em vez de
// gravar o seu por cima.
func TestResolve_CorruptFile_AdoptsRecordedRegeneration(t *testing.T) {
	winner := "0190aaaa-bbbb-7ccc-8ddd-eeeeffff0000"
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	sys.files["/data/agent_uuid"] = "nao-e-um-uuid\n"    // corrompido
	sys.files["/data/.agent_uuid.regen"] = winner + "\n" // outro já registrou

	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentUUID != winner {
		t.Errorf("agentUUID = %q (esperava adotar o registro %q)", id.agentUUID, winner)
	}
	// O valor adotado precisa acabar no arquivo de identidade: é o que faz o
	// processo voltar com a mesma identidade que está no disco.
	if got := sys.files["/data/agent_uuid"]; got != winner+"\n" {
		t.Errorf("arquivo de identidade = %q (esperava %q)", got, winner+"\n")
	}
	if sys.uuidCalls != 1 {
		t.Errorf("gerou %d UUID(s); o valor gerado deveria ter sido descartado em favor do registro", sys.uuidCalls)
	}
}

// Sem registro anterior, este processo é o vencedor: grava o registro E o
// arquivo de identidade com o próprio valor.
func TestResolve_CorruptFile_WinnerRecordsRegeneration(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	sys.files["/data/agent_uuid"] = "nao-e-um-uuid\n"

	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentUUID != sys.uuid {
		t.Errorf("agentUUID = %q (esperava o valor gerado %q)", id.agentUUID, sys.uuid)
	}
	for _, path := range []string{"/data/agent_uuid", "/data/.agent_uuid.regen"} {
		if got := sys.files[path]; got != sys.uuid+"\n" {
			t.Errorf("%s = %q (esperava %q)", path, got, sys.uuid+"\n")
		}
	}
}

// Um registro incompleto (só possível se a criação exclusiva tiver caído no
// plano B e o processo tiver morrido no meio) é descartado, e a regeneração
// segue normalmente em vez de travar.
func TestResolve_CorruptFile_DiscardsIncompleteRecord(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	sys.files["/data/agent_uuid"] = "nao-e-um-uuid\n"
	sys.files["/data/.agent_uuid.regen"] = "" // criado, nunca preenchido

	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentUUID != sys.uuid {
		t.Errorf("agentUUID = %q (esperava o valor gerado %q)", id.agentUUID, sys.uuid)
	}
	if got := sys.files["/data/.agent_uuid.regen"]; got != sys.uuid+"\n" {
		t.Errorf("o registro incompleto não foi refeito: %q", got)
	}
}

// Identidade criada do zero (arquivo AUSENTE) torna obsoleto um registro de
// regeneração anterior, que por isso é removido.
func TestResolve_ColdStart_ClearsStaleRegenerationRecord(t *testing.T) {
	sys := newFakeSystem().withDataDir()
	sys.env["MACHINE_ID"] = "abcdef0123456789abcdef0123456789"
	sys.env["WORKSPACE"] = "prod"
	sys.files["/data/.agent_uuid.regen"] = "0190aaaa-bbbb-7ccc-8ddd-eeeeffff0000\n"

	id, f := resolve(sys)
	if f != nil {
		t.Fatalf("falha inesperada: %+v", f)
	}
	if id.agentUUID != sys.uuid {
		t.Errorf("agentUUID = %q (esperava o valor recém-gerado %q)", id.agentUUID, sys.uuid)
	}
	if _, ok := sys.files["/data/.agent_uuid.regen"]; ok {
		t.Errorf("o registro obsoleto deveria ter sido removido")
	}
}
