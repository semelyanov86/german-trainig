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
	tempFiles   []string
	logger      *log.Logger
}

func New(historyDir string, logger *log.Logger) *Session {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	return &Session{
		ID:          id,
		HistoryFile: filepath.Join(historyDir, fmt.Sprintf("history_%s.txt", id)),
		logger:      logger,
	}
}

func (s *Session) AddTempFiles(files ...string) {
	s.tempFiles = append(s.tempFiles, files...)
}

func (s *Session) WriteHistory(speaker, text string) {
	f, err := os.OpenFile(s.HistoryFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
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
