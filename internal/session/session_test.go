package session

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateSessionPermissionsAndCleanup(t *testing.T) {
	root := t.TempDir()
	s, err := NewPrivate(root, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(s.TempDir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("session directory is not private")
	}
	s.WriteHistory("User", "личная реплика")
	info, err = os.Stat(s.HistoryFile)
	if err != nil || info.Mode().Perm() != 0600 || s.ReadHistory() != "User: личная реплика\n" {
		t.Fatal("transcript permissions or content incorrect")
	}
	partial := filepath.Join(s.TempDir, "partial.wav")
	if err := os.WriteFile(partial, []byte("unfinished provider output"), 0644); err != nil {
		t.Fatal(err)
	}
	s.Cleanup()
	if _, err := os.Stat(s.TempDir); !os.IsNotExist(err) {
		t.Fatal("unregistered partial audio survived cleanup")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("cleanup removed the parent directory")
	}
}
