package loghub_ident

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// writeFile creates a file with content in the given directory.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile(%s): %v", p, err)
	}
	return p
}

// ---------------------------------------------------------------------------
// TestResolveDataDir
// ---------------------------------------------------------------------------

func TestResolveDataDir(t *testing.T) {
	tests := []struct {
		name     string
		setupFn  func(t *testing.T) string // retorna valor para DATADIR env
		wantCode int
	}{
		{
			name: "env aponta para diretório existente",
			setupFn: func(t *testing.T) string {
				return t.TempDir()
			},
			wantCode: 0,
		},
		{
			name: "DATADIR inexistente",
			setupFn: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "nope")
			},
			wantCode: 100,
		},
		{
			name: "DATADIR aponta para arquivo regular",
			setupFn: func(t *testing.T) string {
				d := t.TempDir()
				p := filepath.Join(d, "file.txt")
				os.WriteFile(p, []byte("x"), 0644)
				return p
			},
			wantCode: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setupFn(t)
			t.Setenv("DATADIR", dir)

			got, code, reason := resolveDataDirE()
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d (reason: %s)", code, tt.wantCode, reason)
			}
			if tt.wantCode == 0 && got != dir {
				t.Fatalf("got = %q, want %q", got, dir)
			}
		})
	}
}

func TestResolveDataDir_Fallback(t *testing.T) {
	// Sem env → fallback /data. Na maioria dos ambientes de teste /data não
	// existe, então esperamos código 100.
	t.Setenv("DATADIR", "")
	_, code, _ := resolveDataDirE()
	// /data provavelmente não existe no host de CI
	if code != 100 && code != 0 {
		t.Fatalf("esperava 100 (ou 0 se /data existir), got %d", code)
	}
}

// ---------------------------------------------------------------------------
// TestResolveMachineID
// ---------------------------------------------------------------------------

func TestResolveMachineID(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		fileBody  string
		fileExist bool
		want      string
		wantCode  int
	}{
		{
			name:     "env válido sem hífen",
			env:      "abcdef0123456789abcdef0123456789",
			want:     "abcdef0123456789abcdef0123456789",
			wantCode: 0,
		},
		{
			name:     "env com hífens (formato UUID)",
			env:      "abcdef01-2345-6789-abcd-ef0123456789",
			want:     "abcdef0123456789abcdef0123456789",
			wantCode: 0,
		},
		{
			name:     "env uppercase é normalizado",
			env:      "ABCDEF0123456789ABCDEF0123456789",
			want:     "abcdef0123456789abcdef0123456789",
			wantCode: 0,
		},
		{
			name:      "arquivo válido",
			env:       "",
			fileBody:  "deadbeef0123456789abcdef01234567\n",
			fileExist: true,
			want:      "deadbeef0123456789abcdef01234567",
			wantCode:  0,
		},
		{
			name:      "env vazio sem arquivo",
			env:       "",
			fileBody:  "",
			fileExist: false,
			wantCode:  101,
		},
		{
			name:     "valor inválido curto",
			env:      "xyz",
			wantCode: 102,
		},
		{
			name:     "valor inválido com chars não-hex",
			env:      "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",
			wantCode: 102,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MACHINE_ID", tt.env)

			tmpDir := t.TempDir()
			machineIDPath := filepath.Join(tmpDir, "machine-id")
			if tt.fileExist {
				writeFile(t, tmpDir, "machine-id", tt.fileBody)
			}

			got, code, reason := resolveMachineIDE(machineIDPath)
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d (reason: %s)", code, tt.wantCode, reason)
			}
			if tt.wantCode == 0 && got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestResolveAgentName
// ---------------------------------------------------------------------------

func TestResolveAgentName(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		fileBody string // conteúdo do arquivo agent_name; "" = não criar
		argv0    string
		want     string
		wantCode int
	}{
		{
			name:     "env válido",
			env:      "my-agent",
			argv0:    "bin",
			want:     "my-agent",
			wantCode: 0,
		},
		{
			name:     "arquivo válido",
			env:      "",
			fileBody: "agent-from-file\n",
			argv0:    "bin",
			want:     "agent-from-file",
			wantCode: 0,
		},
		{
			name:     "fallback argv0",
			env:      "",
			fileBody: "",
			argv0:    "/usr/local/bin/My_Agent-v2",
			want:     "my_agent-v2",
			wantCode: 0,
		},
		{
			name:     "argv0 com chars especiais",
			env:      "",
			fileBody: "",
			argv0:    "/path/to/my agent@v1!",
			want:     "myagentv1",
			wantCode: 0,
		},
		{
			name:     "tudo vazio → 103",
			env:      "",
			fileBody: "",
			argv0:    "///",
			wantCode: 103,
		},
		{
			name:     "env com formato inválido → 104",
			env:      "my agent!!!",
			argv0:    "bin",
			wantCode: 104,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AGENT_NAME", tt.env)

			dataDir := t.TempDir()
			if tt.fileBody != "" {
				writeFile(t, dataDir, fileAgentName, tt.fileBody)
			}

			got, code, reason := resolveAgentNameE(dataDir, tt.argv0)
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d (reason: %s)", code, tt.wantCode, reason)
			}
			if tt.wantCode == 0 && got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestResolveAgentUUID
// ---------------------------------------------------------------------------

func TestResolveAgentUUID(t *testing.T) {
	validUUID := "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"

	tests := []struct {
		name           string
		env            string
		fileBody       string // conteúdo de $DATADIR/agent_uuid
		want           string // valor esperado (vazio = qualquer UUID v4 válido)
		wantCode       int
		expectFileWrit bool // espera que o arquivo agent_uuid seja criado
	}{
		{
			name:     "env válido",
			env:      validUUID,
			want:     validUUID,
			wantCode: 0,
		},
		{
			name:     "arquivo válido",
			env:      "",
			fileBody: validUUID + "\n",
			want:     validUUID,
			wantCode: 0,
		},
		{
			name:           "gerar via crypto/rand e gravar",
			env:            "",
			fileBody:       "",
			want:           "", // qualquer UUID v4 válido
			wantCode:       0,
			expectFileWrit: true,
		},
		{
			name:     "env com UUID inválido → 107",
			env:      "not-a-uuid",
			wantCode: 107,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AGENT_UUID", tt.env)

			dataDir := t.TempDir()
			if tt.fileBody != "" {
				writeFile(t, dataDir, fileAgentUUID, tt.fileBody)
			}

			got, code, reason := resolveAgentUUIDE(dataDir)
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d (reason: %s)", code, tt.wantCode, reason)
			}
			if tt.wantCode == 0 {
				if tt.want != "" && got != tt.want {
					t.Fatalf("got = %q, want %q", got, tt.want)
				}
				if tt.want == "" && !reUUIDv4.MatchString(got) {
					t.Fatalf("got = %q, não é UUID v4 válido", got)
				}
			}

			if tt.expectFileWrit {
				p := filepath.Join(dataDir, fileAgentUUID)
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatalf("arquivo agent_uuid não foi criado: %v", err)
				}
				content := string(data)
				// O conteúdo deve ser o UUID gerado + newline
				if content != got+"\n" {
					t.Fatalf("conteúdo do arquivo = %q, want %q", content, got+"\n")
				}
				info, _ := os.Stat(p)
				if perm := info.Mode().Perm(); perm != 0644 {
					t.Fatalf("perm = %o, want 0644", perm)
				}
			}
		})
	}
}

func TestResolveAgentUUID_SecondRunReadsFile(t *testing.T) {
	t.Setenv("AGENT_UUID", "")
	dataDir := t.TempDir()

	// Primeira execução: gera via crypto/rand e grava
	got1, code1, reason1 := resolveAgentUUIDE(dataDir)
	if code1 != 0 {
		t.Fatalf("1ª execução falhou: code=%d reason=%s", code1, reason1)
	}
	if !reUUIDv4.MatchString(got1) {
		t.Fatalf("1ª execução: got=%q não é UUID v4 válido", got1)
	}

	// Verifica que o arquivo foi criado
	p := filepath.Join(dataDir, fileAgentUUID)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("arquivo agent_uuid não foi criado: %v", err)
	}
	if string(data) != got1+"\n" {
		t.Fatalf("conteúdo do arquivo = %q, want %q", string(data), got1+"\n")
	}

	// Segunda execução: deve ler do arquivo, não gerar novamente
	got2, code2, reason2 := resolveAgentUUIDE(dataDir)
	if code2 != 0 {
		t.Fatalf("2ª execução falhou: code=%d reason=%s", code2, reason2)
	}
	if got2 != got1 {
		t.Fatalf("2ª execução: got=%q want=%q (deve ler do arquivo, não gerar novo)", got2, got1)
	}
}

func TestResolveAgentUUID_WriteFailure(t *testing.T) {
	t.Setenv("AGENT_UUID", "")

	// dataDir não gravável
	dataDir := t.TempDir()
	os.Chmod(dataDir, 0555)
	defer os.Chmod(dataDir, 0755)

	_, code, _ := resolveAgentUUIDE(dataDir)
	if code != 106 {
		t.Fatalf("code = %d, want 106", code)
	}
}

// ---------------------------------------------------------------------------
// TestResolveHostname
// ---------------------------------------------------------------------------

func TestResolveHostname(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		wantCode int
	}{
		{
			name:     "env válido",
			env:      "my-host.local",
			wantCode: 0,
		},
		{
			name:     "env uppercase normalizado",
			env:      "MY-HOST",
			wantCode: 0,
		},
		{
			name:     "env inválido → 109",
			env:      "host name!",
			wantCode: 109,
		},
		{
			name:     "fallback os.Hostname",
			env:      "",
			wantCode: 0, // deve funcionar em qualquer CI
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOSTNAME", tt.env)

			got, code, reason := resolveHostnameE()
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d (reason: %s)", code, tt.wantCode, reason)
			}
			if tt.wantCode == 0 && got == "" {
				t.Fatal("got empty hostname")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestResolveWorkspace
// ---------------------------------------------------------------------------

func TestResolveWorkspace(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		fileBody string
		want     string
		wantCode int
	}{
		{
			name:     "env válido",
			env:      "production",
			want:     "production",
			wantCode: 0,
		},
		{
			name:     "arquivo válido",
			env:      "",
			fileBody: "staging\n",
			want:     "staging",
			wantCode: 0,
		},
		{
			name:     "env e arquivo ausentes → 110",
			env:      "",
			fileBody: "",
			wantCode: 110,
		},
		{
			name:     "valor inválido → 111",
			env:      "my workspace!",
			wantCode: 111,
		},
		{
			name:     "env uppercase normalizado",
			env:      "PROD",
			want:     "prod",
			wantCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WORKSPACE", tt.env)

			dataDir := t.TempDir()
			if tt.fileBody != "" {
				writeFile(t, dataDir, fileWorkspace, tt.fileBody)
			}

			got, code, reason := resolveWorkspaceE(dataDir)
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d (reason: %s)", code, tt.wantCode, reason)
			}
			if tt.wantCode == 0 && got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestReadFirstLine
// ---------------------------------------------------------------------------

func TestReadFirstLine(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{"single line no newline", "hello", "hello", false},
		{"single line with newline", "hello\n", "hello", false},
		{"multi line", "first\nsecond\n", "first", false},
		{"whitespace trimmed", "  hello  \n", "hello", false},
		{"empty file", "", "", false},
		{"windows newline", "hello\r\nworld", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			p := writeFile(t, dir, "test.txt", tt.content)

			got, err := readFirstLine(p)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("arquivo inexistente", func(t *testing.T) {
		_, err := readFirstLine(filepath.Join(t.TempDir(), "nope"))
		if err == nil {
			t.Fatal("esperava erro para arquivo inexistente")
		}
	})
}

// ---------------------------------------------------------------------------
// TestGenerateUUIDv4
// ---------------------------------------------------------------------------

func TestGenerateUUIDv4(t *testing.T) {
	t.Run("formato válido", func(t *testing.T) {
		uuid, err := generateUUIDv4()
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		if !reUUIDv4.MatchString(uuid) {
			t.Fatalf("UUID gerado %q não casa com regex de UUID v4", uuid)
		}
		if len(uuid) != 36 {
			t.Fatalf("UUID gerado tem %d chars, esperava 36", len(uuid))
		}
	})

	t.Run("unicidade", func(t *testing.T) {
		seen := make(map[string]bool, 100)
		for i := 0; i < 100; i++ {
			uuid, err := generateUUIDv4()
			if err != nil {
				t.Fatalf("erro na iteração %d: %v", i, err)
			}
			if seen[uuid] {
				t.Fatalf("UUID duplicado na iteração %d: %s", i, uuid)
			}
			seen[uuid] = true
		}
	})

	t.Run("version e variant bits", func(t *testing.T) {
		for i := 0; i < 50; i++ {
			uuid, err := generateUUIDv4()
			if err != nil {
				t.Fatalf("erro: %v", err)
			}
			// Posição 14 deve ser '4' (version)
			if uuid[14] != '4' {
				t.Fatalf("UUID %q: posição 14 = %c, esperava '4'", uuid, uuid[14])
			}
			// Posição 19 deve ser 8, 9, a ou b (variant)
			v := uuid[19]
			if v != '8' && v != '9' && v != 'a' && v != 'b' {
				t.Fatalf("UUID %q: posição 19 = %c, esperava 8/9/a/b", uuid, v)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// TestResolveMachineIDFile
// ---------------------------------------------------------------------------

func TestResolveMachineIDFile(t *testing.T) {
	t.Run("padrão /etc/machine-id", func(t *testing.T) {
		t.Setenv("MACHINE_ID_FILE", "")
		got := resolveMachineIDFile()
		if got != DefaultMachineIDFile {
			t.Fatalf("got = %q, want %q", got, DefaultMachineIDFile)
		}
	})

	t.Run("env override", func(t *testing.T) {
		custom := "/custom/path/machine-id"
		t.Setenv("MACHINE_ID_FILE", custom)
		got := resolveMachineIDFile()
		if got != custom {
			t.Fatalf("got = %q, want %q", got, custom)
		}
	})

	t.Run("env com espaços é trimado", func(t *testing.T) {
		t.Setenv("MACHINE_ID_FILE", "  /custom/path  ")
		got := resolveMachineIDFile()
		if got != "/custom/path" {
			t.Fatalf("got = %q, want %q", got, "/custom/path")
		}
	})
}
