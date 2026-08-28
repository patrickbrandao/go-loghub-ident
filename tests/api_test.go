package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Regexes da SPEC, reescritas aqui de propósito: os testes validam o contrato
// público, não reaproveitam as regexes internas da biblioteca.
var (
	reMachineID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	reAgentName = regexp.MustCompile(`^[a-z0-9._-]+$`)
	reAgentUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	reHostname  = regexp.MustCompile(`^[a-z0-9.-]+$`)
	reWorkspace = regexp.MustCompile(`^[a-z0-9.-]+$`)
)

// ----- Caminho feliz -----

// TestHappyPath_AllFromEnv cobre a promessa central da SPEC §6: com as cinco
// envs presentes, o $DATADIR nunca é tocado e sua ausência não é fatal.
func TestHappyPath_AllFromEnv(t *testing.T) {
	env := fullEnv()
	env["MACHINE_ID"] = "ABCDEF0123456789ABCDEF0123456789" // maiúsculas
	env["AGENT_NAME"] = "My-Service"
	env["AGENT_UUID"] = "019E99E3-42F0-7882-9719-2305FF84949C"
	env["HOSTNAME"] = "Node01"
	env["WORKSPACE"] = "Production"

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d (esperava 0)\nstderr: %s", res.code, res.stderr)
	}

	want := map[string]string{
		"MACHINE_ID": "abcdef0123456789abcdef0123456789",
		"AGENT_NAME": "my-service",
		"AGENT_UUID": "019e99e3-42f0-7882-9719-2305ff84949c",
		"HOSTNAME":   "node01",
		"WORKSPACE":  "production",
	}
	for k, v := range want {
		if got := res.field(t, k); got != v {
			t.Errorf("%s = %q (esperava %q — normalização para lowercase)", k, got, v)
		}
	}

	// O diretório apontado por DATADIR não existe; nada pode ter sido criado.
	if _, err := os.Stat(env["DATADIR"]); !os.IsNotExist(err) {
		t.Errorf("$DATADIR foi tocado no caminho feliz: %v", err)
	}
}

// TestHappyPath_MachineIDHyphensStripped confirma a regra de saneamento
// específica do MACHINE_ID (SPEC §5): só nele os hífens são removidos.
func TestHappyPath_MachineIDHyphensStripped(t *testing.T) {
	env := fullEnv()
	env["MACHINE_ID"] = "019e99e3-42f0-7882-9719-2305ff84949c" // formato UUID
	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if got := res.field(t, "MACHINE_ID"); got != "019e99e342f0788297192305ff84949c" {
		t.Errorf("MACHINE_ID = %q", got)
	}
}

// TestFormats verifica que todo valor entregue pelos getters passa na regex
// do seu campo, mesmo quando vem de geração ou de fallback.
func TestFormats(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID", "AGENT_NAME", "AGENT_UUID", "WORKSPACE")
	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	for _, c := range []struct {
		key string
		re  *regexp.Regexp
	}{
		{"MACHINE_ID", reMachineID},
		{"AGENT_NAME", reAgentName},
		{"AGENT_UUID", reAgentUUID},
		{"HOSTNAME", reHostname},
		{"WORKSPACE", reWorkspace},
	} {
		if v := res.field(t, c.key); !c.re.MatchString(v) {
			t.Errorf("%s = %q não casa com %s", c.key, v, c.re)
		}
	}
}

// ----- Precedência env > arquivo > fallback -----

// TestPrecedence_EnvBeatsFile garante que a env vence o arquivo de $DATADIR e
// que a presença do arquivo não altera o valor entregue.
func TestPrecedence_EnvBeatsFile(t *testing.T) {
	env := withDataDir(t)
	dir := env["DATADIR"]
	writeFile(t, dir, "agent_name", "do-arquivo\n")
	writeFile(t, dir, "workspace", "do-arquivo\n")
	writeFile(t, dir, "machine_id", "11112222333344445555666677778888\n")
	writeFile(t, dir, "agent_uuid", "01900000-0000-7000-8000-000000000abc\n")

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if got := res.field(t, "AGENT_NAME"); got != "svc" {
		t.Errorf("AGENT_NAME = %q (env deveria vencer o arquivo)", got)
	}
	if got := res.field(t, "WORKSPACE"); got != "prod" {
		t.Errorf("WORKSPACE = %q (env deveria vencer o arquivo)", got)
	}
	if got := res.field(t, "MACHINE_ID"); got != "abcdef0123456789abcdef0123456789" {
		t.Errorf("MACHINE_ID = %q (env deveria vencer o arquivo)", got)
	}
}

// TestPrecedence_FileBeatsFallback garante que o arquivo em $DATADIR vence os
// fallbacks determinísticos (argv[0] e "default").
func TestPrecedence_FileBeatsFallback(t *testing.T) {
	env := without(withDataDir(t), "AGENT_NAME", "WORKSPACE")
	dir := env["DATADIR"]
	writeFile(t, dir, "agent_name", "Edge-Collector\n") // lowercase aplicado
	writeFile(t, dir, "workspace", "Staging\n")

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if got := res.field(t, "AGENT_NAME"); got != "edge-collector" {
		t.Errorf("AGENT_NAME = %q", got)
	}
	if got := res.field(t, "WORKSPACE"); got != "staging" {
		t.Errorf("WORKSPACE = %q", got)
	}
}

// TestFallback_Argv0 cobre o nível 3 do AGENT_NAME (SPEC §8): base de argv[0],
// lowercase, sem o sufixo ".exe" — e sem gravar arquivo.
func TestFallback_Argv0(t *testing.T) {
	env := without(withDataDir(t), "AGENT_NAME")
	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if got, want := res.field(t, "AGENT_NAME"), helperAgentName(t); got != want {
		t.Errorf("AGENT_NAME = %q (esperava %q, de argv[0])", got, want)
	}
	if _, err := os.Stat(filepath.Join(env["DATADIR"], "agent_name")); err == nil {
		t.Error("agent_name vindo de argv[0] não deveria ser persistido (SPEC §8)")
	}
}

// TestFallback_WorkspaceDefault cobre o nível 3 do WORKSPACE (SPEC §11).
func TestFallback_WorkspaceDefault(t *testing.T) {
	env := without(withDataDir(t), "WORKSPACE")
	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if got := res.field(t, "WORKSPACE"); got != "default" {
		t.Errorf("WORKSPACE = %q (esperava default)", got)
	}
	if _, err := os.Stat(filepath.Join(env["DATADIR"], "workspace")); err == nil {
		t.Error("o fallback \"default\" não deveria gravar arquivo (SPEC §11)")
	}
}

// TestFallback_HostnameFromOS cobre o nível 2 do HOSTNAME: sem a env, o valor
// vem de os.Hostname() já em lowercase.
func TestFallback_HostnameFromOS(t *testing.T) {
	env := without(withDataDir(t), "HOSTNAME")
	res := run(t, env)
	if res.code != 0 {
		t.Skipf("hostname da máquina não passa na regex da SPEC; nada a validar (stderr: %s)", res.stderr)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Skipf("os.Hostname(): %v", err)
	}
	if got, want := res.field(t, "HOSTNAME"), strings.ToLower(strings.TrimSpace(host)); got != want {
		t.Errorf("HOSTNAME = %q (esperava %q)", got, want)
	}
}

// TestMachineIDFile cobre o nível 2 do MACHINE_ID: leitura de $MACHINE_ID_FILE.
func TestMachineIDFile(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID")
	mid := filepath.Join(env["DATADIR"], "custom-machine-id")
	if err := os.WriteFile(mid, []byte("11112222333344445555666677778888\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env["MACHINE_ID_FILE"] = mid

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	if got := res.field(t, "MACHINE_ID"); got != "11112222333344445555666677778888" {
		t.Errorf("MACHINE_ID = %q", got)
	}
	// Valor lido do arquivo do SO não é copiado para $DATADIR (SPEC §7).
	if _, err := os.Stat(filepath.Join(env["DATADIR"], "machine_id")); err == nil {
		t.Error("machine_id de $MACHINE_ID_FILE não deveria ser persistido em $DATADIR")
	}
}

// TestMachineIDFile_MissingFallsBackToDefault cobre a regra da SPEC §7.2: env
// apontando para arquivo inexistente troca para /etc/machine-id.
func TestMachineIDFile_MissingFallsBackToDefault(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID")
	env["MACHINE_ID_FILE"] = filepath.Join(t.TempDir(), "nao-existe")

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	// Sem /etc/machine-id legível a cadeia termina em geração; com ele, o valor
	// vem de lá. Os dois desfechos são válidos: o que importa é não abortar.
	if !reMachineID.MatchString(res.field(t, "MACHINE_ID")) {
		t.Errorf("MACHINE_ID = %q não casa com a regex", res.field(t, "MACHINE_ID"))
	}
}

// ----- Persistência -----

// TestPersistence_GeneratesAndReuses é o teste de identidade estável: a
// primeira execução gera e grava machine_id e agent_uuid; a segunda deve
// reobter exatamente os mesmos valores do disco, sem gerar de novo.
func TestPersistence_GeneratesAndReuses(t *testing.T) {
	env := without(withDataDir(t), "MACHINE_ID", "AGENT_UUID")
	dir := env["DATADIR"]

	first := run(t, env)
	if first.code != 0 {
		t.Fatalf("1ª execução: exit=%d\nstderr: %s", first.code, first.stderr)
	}

	gotMID, gotUUID := first.field(t, "MACHINE_ID"), first.field(t, "AGENT_UUID")
	if disk := readIdentFile(t, dir, "machine_id"); disk != gotMID {
		t.Errorf("machine_id em disco = %q, processo usou %q", disk, gotMID)
	}
	if disk := readIdentFile(t, dir, "agent_uuid"); disk != gotUUID {
		t.Errorf("agent_uuid em disco = %q, processo usou %q", disk, gotUUID)
	}

	second := run(t, env)
	if second.code != 0 {
		t.Fatalf("2ª execução: exit=%d\nstderr: %s", second.code, second.stderr)
	}
	if got := second.field(t, "MACHINE_ID"); got != gotMID {
		t.Errorf("MACHINE_ID mudou entre execuções: %q -> %q", gotMID, got)
	}
	if got := second.field(t, "AGENT_UUID"); got != gotUUID {
		t.Errorf("AGENT_UUID mudou entre execuções: %q -> %q", gotUUID, got)
	}
}

// TestPersistence_CorruptUUIDRegenerates cobre a exceção da SPEC §5: os
// arquivos auto-geridos com conteúdo inválido são tratados como ausentes.
func TestPersistence_CorruptUUIDRegenerates(t *testing.T) {
	env := without(withDataDir(t), "AGENT_UUID")
	dir := env["DATADIR"]
	writeFile(t, dir, "agent_uuid", "xxxx-nao-e-um-uuid\n")

	res := run(t, env)
	if res.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", res.code, res.stderr)
	}
	got := res.field(t, "AGENT_UUID")
	if !reAgentUUID.MatchString(got) {
		t.Errorf("AGENT_UUID = %q não é um UUIDv7 canônico", got)
	}
	if disk := readIdentFile(t, dir, "agent_uuid"); disk != got {
		t.Errorf("arquivo corrompido não foi regravado: disco=%q, processo=%q", disk, got)
	}
}

// ----- Diagnóstico -----

// TestDebugOutput confirma que LOGHUB_IDENT_DEBUG produz linhas de origem em
// stderr e que o modo silencioso é o padrão.
func TestDebugOutput(t *testing.T) {
	env := withDataDir(t)

	quiet := run(t, env)
	if quiet.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", quiet.code, quiet.stderr)
	}
	if quiet.stderr != "" {
		t.Errorf("sem LOGHUB_IDENT_DEBUG a lib deveria ser silenciosa; stderr:\n%s", quiet.stderr)
	}

	env["LOGHUB_IDENT_DEBUG"] = "1"
	loud := run(t, env)
	if loud.code != 0 {
		t.Fatalf("exit=%d\nstderr: %s", loud.code, loud.stderr)
	}
	for _, want := range []string{
		"MACHINE_ID: env", "AGENT_NAME: env", "AGENT_UUID: env",
		"HOSTNAME: env", "WORKSPACE: env",
	} {
		if !strings.Contains(loud.stderr, want) {
			t.Errorf("linha de debug ausente: %q\nstderr:\n%s", want, loud.stderr)
		}
	}
}

// writeFile grava um arquivo dentro de $DATADIR para montar o cenário.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("gravação de %s: %v", name, err)
	}
}
