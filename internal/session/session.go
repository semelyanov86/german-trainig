package session

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type Session struct {
	ID          string
	HistoryFile string
	TempDir     string
	SpokenRole  string // optional: record only successfully played assistant lines
	tempFiles   []string
	logger      *log.Logger
	privateDir  bool
}

func New(historyDir string, logger *log.Logger) *Session {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	return &Session{
		ID:          id,
		HistoryFile: filepath.Join(historyDir, fmt.Sprintf("history_%s.txt", id)),
		TempDir:     "/tmp",
		logger:      logger,
	}
}

// NewPrivate isolates transcripts and all temporary audio from other users.
// The directory is also the cleanup boundary for partial provider output.
func NewPrivate(historyDir string, logger *log.Logger) (*Session, error) {
	dir, err := os.MkdirTemp(historyDir, "session_")
	if err != nil {
		return nil, fmt.Errorf("create private session directory: %w", err)
	}
	s := New(historyDir, logger)
	s.TempDir = dir
	s.HistoryFile = filepath.Join(dir, "history.txt")
	s.privateDir = true
	return s, nil
}

func (s *Session) AddTempFiles(files ...string) {
	s.tempFiles = append(s.tempFiles, files...)
}

func (s *Session) WriteHistory(speaker, text string) {
	f, err := os.OpenFile(s.HistoryFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		s.logger.Printf("ERROR writing history: %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s: %s\n", speaker, text)
}

func (s *Session) ReadHistory() string {
	data, err := os.ReadFile(s.HistoryFile)
	if err != nil {
		return ""
	}
	return string(data)
}

func (s *Session) Cleanup() {
	s.logger.Println("Cleaning up session files...")
	if s.privateDir {
		if err := os.RemoveAll(s.TempDir); err != nil {
			s.logger.Printf("ERROR cleaning private session: %v", err)
		}
		s.logger.Println("Cleanup complete")
		return
	}
	os.Remove(s.HistoryFile)
	for _, f := range s.tempFiles {
		os.Remove(f)
	}
	matches, _ := filepath.Glob(fmt.Sprintf("/tmp/*%s*", s.ID))
	for _, m := range matches {
		os.Remove(m)
	}
	s.logger.Println("Cleanup complete")
}
