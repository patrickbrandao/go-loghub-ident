package loghubident

import (
	"errors"
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

	readErr   map[string]error // caminho -> erro forçado na leitura
	writeErr  map[string]error // caminho -> erro forçado na gravação
	written   map[string]string
	uuidCalls int
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

func (f *fakeSystem) WriteFile(path string, data []byte, _ os.FileMode) error {
	if err := f.writeErr[path]; err != nil {
		return err
	}
	f.files[path] = string(data)
	f.written[path] = string(data)
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
