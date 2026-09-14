package loghubident

import "testing"

// Benchmarks dos validadores e auxiliares REAIS do pacote (tests/bench_test.go
// só alcança a API pública e por isso reimplementa os validadores). São estes
// números que sustentam as metas de docs/07.
//
// Rodar:
//
//	go test . -bench=. -benchmem -run=XXX

func BenchmarkValidMachineID(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBool = validMachineID("abcdef0123456789abcdef0123456789")
	}
}

func BenchmarkValidAgentUUID(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBool = validAgentUUID("019e99e3-42f0-7882-9719-2305ff84949c")
	}
}

func BenchmarkValidAgentName(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBool = validAgentName("edge-collector.v2")
	}
}

func BenchmarkValidWorkspace(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBool = validWorkspace("tenant.sub-1")
	}
}

func BenchmarkValidHostname(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBool = validHostname("web-1.svc.cluster.local")
	}
}

var sinkString string

func BenchmarkFirstLine(b *testing.B) {
	data := append(append([]byte{}, bomUTF8...), "  019e99e3-42f0-7882-9719-2305ff84949c \r\nsegunda linha\n"...)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkString = firstLine(data)
	}
}

func BenchmarkNormalizeMachineID(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkString = normalizeMachineID("ABCDEF01-2345-6789-ABCD-EF0123456789")
	}
}
