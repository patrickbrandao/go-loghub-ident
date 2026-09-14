package loghubident

import (
	"strings"
	"testing"
)

// Testes diretos dos auxiliares de identity.go e resolve.go. A maioria já era
// exercitada por dentro de resolve; aqui cada função ganha o seu próprio
// contrato, para que uma mudança de comportamento apareça no teste da função
// e não como efeito colateral distante num teste de resolução.

// ----- preview: o que uma mensagem reproduz de um valor de ENV -----

func TestPreview(t *testing.T) {
	short := strings.Repeat("a", maxEchoLen)
	if got, want := preview(short), `"`+short+`"`; got != want {
		t.Errorf("preview(curto) = %s (esperava %s)", got, want)
	}

	long := strings.Repeat("b", maxEchoLen+10)
	got := preview(long)
	if !strings.HasPrefix(got, `"`+strings.Repeat("b", maxEchoLen)+`"…`) {
		t.Errorf("preview(longo) não corta no teto: %s", got)
	}
	if !strings.Contains(got, "(74 bytes)") {
		t.Errorf("preview(longo) não informa o tamanho real: %s", got)
	}
	if strings.Contains(got, strings.Repeat("b", maxEchoLen+1)) {
		t.Errorf("preview(longo) reproduziu além do teto: %s", got)
	}

	// Caracteres de controle saem escapados (%q): nunca vão crus para stderr.
	if got := preview("a\x00b\n"); got != `"a\x00b\n"` {
		t.Errorf("preview(controle) = %s", got)
	}
}

// ----- hasControlBytes: caminhos com controle são recusados -----

func TestHasControlBytes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false},
		{"/data", false},
		{"/data com espaço", false},
		{"/dados/ação", false}, // bytes UTF-8 >= 0x80 não são controle
		{"/data\n", true},
		{"/data\r", true},
		{"/data\x00", true},
		{"\x1f/data", true},
		{"/data\x7f", true}, // DEL
	} {
		if got := hasControlBytes(tc.in); got != tc.want {
			t.Errorf("hasControlBytes(%q) = %v (esperava %v)", tc.in, got, tc.want)
		}
	}
}

// ----- normalizeMachineID: lowercase + remoção de hífens -----

func TestNormalizeMachineID(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ABCDEF01-2345-6789-ABCD-EF0123456789", "abcdef0123456789abcdef0123456789"},
		{"abcdef0123456789abcdef0123456789", "abcdef0123456789abcdef0123456789"},
		{"", ""},
		{"---", ""},
		{"a-b_c", "ab_c"}, // só o hífen é removido; o resto fica para a validação
	} {
		if got := normalizeMachineID(tc.in); got != tc.want {
			t.Errorf("normalizeMachineID(%q) = %q (esperava %q)", tc.in, got, tc.want)
		}
	}
}

// ----- isSpaceOrControl: o predicado do trim de firstLine -----

func TestIsSpaceOrControl(t *testing.T) {
	for _, r := range []rune{' ', '\t', '\n', '\r', '\x00', '\x7f', '\u00a0', '\u2028'} {
		if !isSpaceOrControl(r) {
			t.Errorf("%q deveria ser aparado", r)
		}
	}
	for _, r := range []rune{'a', 'Z', '0', '-', '.', '_', 'ç', '日'} {
		if isSpaceOrControl(r) {
			t.Errorf("%q não deveria ser aparado", r)
		}
	}
}

// ----- isRelPathComponent: só "." e ".." -----

func TestIsRelPathComponent(t *testing.T) {
	for _, s := range []string{".", ".."} {
		if !isRelPathComponent(s) {
			t.Errorf("%q deveria ser componente relativo", s)
		}
	}
	for _, s := range []string{"", "...", ".a", "a.", "a", "./"} {
		if isRelPathComponent(s) {
			t.Errorf("%q não deveria ser componente relativo", s)
		}
	}
}

// ----- classes de bytes usadas pelos validadores -----

func TestByteClasses(t *testing.T) {
	for b := 0; b < 256; b++ {
		c := byte(b)
		wantHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if got := isHexLower(c); got != wantHex {
			t.Errorf("isHexLower(%q) = %v (esperava %v)", c, got, wantHex)
		}
		wantAlnum := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')
		if got := isAlnumLower(c); got != wantAlnum {
			t.Errorf("isAlnumLower(%q) = %v (esperava %v)", c, got, wantAlnum)
		}
	}
}

// ----- validHostname: as fronteiras exatas da RFC 1123 -----

func TestValidHostname_Boundaries(t *testing.T) {
	label63 := strings.Repeat("a", maxLabelLen)
	// 63 + 1 + 63 + 1 + 63 + 1 + 61 = 253: o maior hostname aceitável.
	longest := label63 + "." + label63 + "." + label63 + "." + strings.Repeat("a", 61)
	if len(longest) != maxHostnameLen {
		t.Fatalf("hostname de teste tem %d bytes; esperava %d", len(longest), maxHostnameLen)
	}
	checkValidator(t, validHostname,
		[]string{longest, label63, "a", "a.b", "0.0.0.0", "x-y.z", "a1-b2.c3"},
		[]string{
			longest + "a",      // 254 bytes
			label63 + "a",      // rótulo de 64
			label63 + "a.b",    // rótulo de 64 no meio
			".a", "a.", "a..b", // rótulos vazios
			"-a", "a-", // hífen nas bordas
			"a.-b", "a.b-", // hífen nas bordas de um rótulo interno
			"A", "a.B", "a_b", // fora de [a-z0-9.-]
			"a b", "a/b", "a\n", // separadores e controle
		})
}

// ----- validLabel: o rótulo isolado -----

func TestValidLabel(t *testing.T) {
	checkValidator(t, validLabel,
		[]string{"a", "0", "a-b", "a--b", strings.Repeat("z", maxLabelLen)},
		[]string{"", "-", "-a", "a-", "a.b", "A", strings.Repeat("z", maxLabelLen+1)})
}

// ----- Promessa de zero alocações dos validadores (docs/07) -----

var sinkBool bool

func TestValidators_ZeroAllocs(t *testing.T) {
	cases := map[string]func() bool{
		"validMachineID": func() bool {
			return validMachineID("abcdef0123456789abcdef0123456789") && !validMachineID("xyz")
		},
		"validAgentUUID": func() bool {
			return validAgentUUID("019e99e3-42f0-7882-9719-2305ff84949c") &&
				!validAgentUUID("019e99e3-42f0-4882-9719-2305ff84949c")
		},
		"validAgentName": func() bool {
			return validAgentName("edge-collector.v2") && !validAgentName("..")
		},
		"validWorkspace": func() bool {
			return validWorkspace("tenant.sub-1") && !validWorkspace("com_underscore")
		},
		"validHostname": func() bool {
			return validHostname("web-1.svc.cluster.local") && !validHostname("-host-.x")
		},
		"rootedPath": func() bool {
			return rootedPath("/data") && !rootedPath("dados")
		},
		"hasControlBytes": func() bool {
			return !hasControlBytes("/var/lib/app") && hasControlBytes("/x\n")
		},
	}
	for name, fn := range cases {
		if allocs := testing.AllocsPerRun(1000, func() { sinkBool = fn() }); allocs != 0 {
			t.Errorf("%s alocou %.0f vez(es) por chamada; a promessa é zero", name, allocs)
		}
	}
}
