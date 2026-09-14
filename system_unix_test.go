//go:build unix

package loghubident

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Garantias de openRegular que só existem em Unix: abrir sem bloquear num
// FIFO e recusar um link simbólico no próprio open (O_NOFOLLOW).

func TestOpenRegular_FIFODoesNotBlock(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo indisponível: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		f, err := openRegular(fifo, true)
		if err == nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openRegular bloqueou num FIFO sem escritor (BUG-01)")
	}
}

func TestOpenRegular_NoFollowRefusesSymlink(t *testing.T) {
	if openNoFollow == 0 {
		t.Skip("plataforma sem O_NOFOLLOW no pacote syscall")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "alvo")
	if err := os.WriteFile(target, []byte("alvo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// follow=false: o kernel recusa, e o erro é reconhecido como recusa de link.
	f, err := openRegular(link, false)
	if err == nil {
		f.Close()
		t.Fatal("openRegular(follow=false) abriu através do link simbólico")
	}
	if !isSymlinkRefusal(err) {
		t.Errorf("erro %v não é reconhecido por isSymlinkRefusal", err)
	}

	// follow=true: o mesmo link é seguido (fontes do sistema podem ser links).
	f, err = openRegular(link, true)
	if err != nil {
		t.Fatalf("openRegular(follow=true) recusou o link: %v", err)
	}
	f.Close()

	// E o classificador não confunde outros erros com recusa de link.
	if isSymlinkRefusal(os.ErrNotExist) || isSymlinkRefusal(nil) {
		t.Error("isSymlinkRefusal reconheceu um erro que não é de link")
	}
}
