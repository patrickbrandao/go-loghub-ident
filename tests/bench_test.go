package tests

import (
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	lhident "github.com/patrickbrandao/go-loghub-ident"
)

// Benchmarks que sustentam as medições de tasks/REPORT-01.md.
//
// Rodar:
//
//	go test ./tests/ -bench=. -benchmem -run=XXX

// ----- Custo de leitura dos getters (o que a SPEC §2 promete ser quase nulo) -----

var initOnce sync.Once

// ensureInitialized chama Initialize() com um ambiente inteiramente controlado.
// Todos os seis campos vêm de env e são válidos, então a resolução não pode
// falhar — nenhum os.Exit inesperado derruba a bateria de benchmarks.
func ensureInitialized(b *testing.B) {
	b.Helper()
	initOnce.Do(func() {
		for k, v := range map[string]string{
			"DATADIR":    b.TempDir(),
			"MACHINE_ID": "abcdef0123456789abcdef0123456789",
			"AGENT_NAME": "svc",
			"AGENT_UUID": "019e99e3-42f0-7882-9719-2305ff84949c",
			"HOSTNAME":   "node01",
			"WORKSPACE":  "prod",
		} {
			if err := os.Setenv(k, v); err != nil {
				b.Fatalf("setenv %s: %v", k, err)
			}
		}
		lhident.Initialize()
	})
}

var sinkString string

func BenchmarkGetter_AgentUUID(b *testing.B) {
	ensureInitialized(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = lhident.AgentUUID()
	}
}

func BenchmarkGetter_AllSix(b *testing.B) {
	ensureInitialized(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkString = lhident.DataDir()
		sinkString = lhident.MachineID()
		sinkString = lhident.AgentName()
		sinkString = lhident.AgentUUID()
		sinkString = lhident.Hostname()
		sinkString = lhident.Workspace()
	}
}

// BenchmarkGetter_Parallel reproduz o cenário citado na SPEC: muitas goroutines
// lendo os getters ao mesmo tempo.
func BenchmarkGetter_Parallel(b *testing.B) {
	ensureInitialized(b)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var local string
		for pb.Next() {
			local = lhident.AgentName()
		}
		sinkString = local
	})
}

// ----- PERF-01: custo do pacote regexp na validação -----
//
// As regexes da biblioteca são compiladas na carga do pacote e usadas poucas
// vezes por boot. O que estes benchmarks quantificam é o preço de manter a
// dependência: o custo fixo de compilação, pago por todo binário que importa a
// biblioteca mesmo que nunca chame Initialize(), e o custo por validação.

var (
	reBenchMachineID = regexp.MustCompile(`^[0-9a-f]{32}$`)
	reBenchAgentName = regexp.MustCompile(`^[a-z0-9._-]+$`)
	reBenchAgentUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

const (
	sampleMachineID = "abcdef0123456789abcdef0123456789"
	sampleAgentName = "edge-collector.v2"
	sampleAgentUUID = "019e99e3-42f0-7882-9719-2305ff84949c"
)

// isHex32 é o equivalente manual de ^[0-9a-f]{32}$.
func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// isAgentName é o equivalente manual de ^[a-z0-9._-]+$.
func isAgentName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

var sinkBool bool

func BenchmarkValidate_MachineID_Regex(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sinkBool = reBenchMachineID.MatchString(sampleMachineID)
	}
}

func BenchmarkValidate_MachineID_Manual(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sinkBool = isHex32(sampleMachineID)
	}
}

func BenchmarkValidate_AgentName_Regex(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sinkBool = reBenchAgentName.MatchString(sampleAgentName)
	}
}

func BenchmarkValidate_AgentName_Manual(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sinkBool = isAgentName(sampleAgentName)
	}
}

func BenchmarkValidate_AgentUUID_Regex(b *testing.B) {
	for i := 0; i < b.N; i++ {
		sinkBool = reBenchAgentUUID.MatchString(sampleAgentUUID)
	}
}

// BenchmarkPackageInit_CompileRegexes mede o custo pago na carga do pacote por
// TODO binário que importa a biblioteca, chame ou não Initialize().
func BenchmarkPackageInit_CompileRegexes(b *testing.B) {
	patterns := []string{
		`^[0-9a-f]{32}$`,
		`^[a-z0-9._-]+$`,
		`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
		`^[a-z0-9.-]+$`,
		`^[a-z0-9.-]+$`,
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for _, p := range patterns {
			sinkRegexp = regexp.MustCompile(p)
		}
	}
}

var sinkRegexp *regexp.Regexp

// ----- Custo de um boot completo (processo separado) -----

// BenchmarkBoot_AllFromEnv mede o boot inteiro, inclusive o fork/exec do
// processo. O número absoluto inclui o custo do sistema operacional; serve para
// comparar variantes da biblioteca entre si, não como custo puro de Initialize.
func BenchmarkBoot_AllFromEnv(b *testing.B) {
	env := map[string]string{
		"DATADIR":    b.TempDir(),
		"MACHINE_ID": sampleMachineID,
		"AGENT_NAME": "svc",
		"AGENT_UUID": sampleAgentUUID,
		"HOSTNAME":   "node01",
		"WORKSPACE":  "prod",
	}
	// runBench dispensa o *testing.T que o harness normalmente exige. Fabricar
	// um &testing.T{} aqui seria comportamento indefinido.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		code, stderr := runBench(b, env)
		if code != 0 {
			b.Fatalf("exit=%d\n%s", code, stderr)
		}
	}
	b.StopTimer()
	sinkString = strings.TrimSpace(sampleAgentName)
}
