package filesync

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"aplaud/internal/protocol"
)

type WriteFunc func([]byte) error

type FileSyncManager struct {
	decryptNotify func([]byte) ([]byte, error)
	encryptedWrite WriteFunc
	notifications  [][]byte
	notifyCh       chan []byte
	fileChunks     map[uint32][]fileChunk
	currentSession *uint32
	downloadDone   bool
	fileListReady   bool
	dataReady      bool
}

type fileChunk struct {
	offset   uint32
	payload  []byte
}

func NewFileSyncManager(decryptNotify func([]byte) ([]byte, error), encryptedWrite WriteFunc) *FileSyncManager {
	return &FileSyncManager{
		decryptNotify:  decryptNotify,
		encryptedWrite: encryptedWrite,
		notifyCh:       make(chan []byte, 256),
		fileChunks:     make(map[uint32][]fileChunk),
	}
}

func (m *FileSyncManager) HandleNotification(data []byte) {
	decrypted, err := m.decryptNotify(data)
	if err != nil {
		fmt.Printf("  [FileSync] decrypt failed: %v\n", err)
		m.notifications = append(m.notifications, data)
		return
	}
	m.notifications = append(m.notifications, decrypted)

	if len(decrypted) < 1 {
		return
	}

	protoType := decrypted[0]
	switch {
	case protoType == 0x02:
		m.handleFileData(decrypted)
	case protoType == 0x01 && len(decrypted) >= 3:
		cmdID := binary.LittleEndian.Uint16(decrypted[1:3])
		switch cmdID {
		case 26:
			m.fileListReady = true
		case 29:
			m.downloadDone = true
			m.dataReady = true
		}
	}
}

func (m *FileSyncManager) handleFileData(data []byte) {
	if m.currentSession == nil || len(data) < 7 {
		return
	}
	fd := protocol.ParseFileDataNotification(data)
	if fd.SessionID != *m.currentSession {
		return
	}
	if fd.IsLast {
		m.downloadDone = true
		m.dataReady = true
		return
	}
	m.fileChunks[fd.SessionID] = append(m.fileChunks[fd.SessionID], fileChunk{
		offset:  fd.FileOffset,
		payload: fd.Payload,
	})
	m.dataReady = true
}

func (m *FileSyncManager) clear() {
	m.notifications = nil
	m.fileListReady = false
	m.dataReady = false
	m.downloadDone = false
}

func (m *FileSyncManager) GetFileList(sessionID uint32, timeout time.Duration) (*protocol.FileListResponse, error) {
	m.clear()
	if sessionID == 0 {
		sessionID = uint32(time.Now().Unix()) & 0xFFFFFFFF
	}

	pkt := protocol.BuildGetRecSessions(sessionID, 0, false)
	fmt.Printf("  [FileSync] Requesting file list (session_id=%d)...\n", sessionID)
	if err := m.encryptedWrite(pkt); err != nil {
		return nil, fmt.Errorf("sending file list request: %w", err)
	}

	deadline := time.After(timeout)
WAIT:
	for {
		select {
		case <-deadline:
			break WAIT
		case <-time.After(100 * time.Millisecond):
			if m.fileListReady {
				break WAIT
			}
		}
	}

	var relevant [][]byte
	for _, n := range m.notifications {
		if len(n) >= 3 && n[0] == 0x01 {
			cmdID := binary.LittleEndian.Uint16(n[1:3])
			if cmdID == 26 {
				relevant = append(relevant, n)
			}
		}
	}

	if len(relevant) == 0 {
		fmt.Println("  [FileSync] No cmd-26 responses found")
		return &protocol.FileListResponse{}, nil
	}

	allData := make([]byte, 0)
	for _, r := range relevant {
		allData = append(allData, r...)
	}

	return protocol.ParseFileListResponse(allData, 20), nil
}

func (m *FileSyncManager) DownloadFile(sessionID uint32, fileSize uint32, outputPath string) ([]byte, error) {
	m.clear()
	m.currentSession = &sessionID
	m.downloadDone = false
	delete(m.fileChunks, sessionID)

	pkt := protocol.BuildSyncFileStart(sessionID, 0, fileSize)
	fmt.Printf("  [FileSync] Starting download session_id=%d, size=%d\n", sessionID, fileSize)
	if err := m.encryptedWrite(pkt); err != nil {
		return nil, fmt.Errorf("sending sync start: %w", err)
	}

	headReceived := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !headReceived {
		time.Sleep(100 * time.Millisecond)
		for _, n := range m.notifications {
			if len(n) >= 7 && n[0] == 0x01 {
				cmdID := binary.LittleEndian.Uint16(n[1:3])
				if cmdID == 28 {
					head := protocol.ParseSyncFileHead(n)
					fmt.Printf("  [FileSync] Head: %s\n", head)
					if !head.OK() {
						return nil, fmt.Errorf("file sync head failed: status=%d", head.Status)
					}
					headReceived = true
					break
				}
			}
		}
	}

	if !headReceived {
		return nil, fmt.Errorf("no file sync head response received")
	}

	fmt.Println("  [FileSync] Receiving data...")
	for !m.downloadDone {
		time.Sleep(100 * time.Millisecond)
		if time.Since(deadline) > 30*time.Second {
			break
		}
	}

	chunks, ok := m.fileChunks[sessionID]
	if !ok || len(chunks) == 0 {
		return nil, fmt.Errorf("no data chunks received")
	}

	allPayload := make([]byte, 0)
	for _, c := range chunks {
		allPayload = append(allPayload, c.payload...)
	}

	var tailCRC *uint16
	for _, n := range m.notifications {
		if len(n) >= 9 && n[0] == 0x01 {
			cmdID := binary.LittleEndian.Uint16(n[1:3])
			if cmdID == 29 {
				tail := protocol.ParseSyncFileTail(n)
				tailCRC = &tail.CRC
				fmt.Printf("  [FileSync] Tail: %s\n", tail)
			}
		}
	}

	if tailCRC != nil && *tailCRC != 0xFFFF {
		computed := protocol.CRC16(allPayload, 0)
		if computed != *tailCRC {
			fmt.Printf("  [FileSync] CRC MISMATCH (computed=0x%04X, expected=0x%04X)\n", computed, *tailCRC)
		} else {
			fmt.Printf("  [FileSync] CRC OK (0x%04X)\n", computed)
		}
	}

	if fileSize > 0 && uint32(len(allPayload)) != fileSize {
		fmt.Printf("  [FileSync] WARNING: size mismatch (expected %d, got %d)\n", fileSize, len(allPayload))
	}

	if outputPath != "" {
		os.MkdirAll(filepath.Dir(outputPath), 0755)
		if err := os.WriteFile(outputPath, allPayload, 0644); err != nil {
			return nil, fmt.Errorf("writing file: %w", err)
		}
		fmt.Printf("  [FileSync] Saved to %s (%d bytes)\n", outputPath, len(allPayload))
	}

	m.currentSession = nil
	return allPayload, nil
}

func (m *FileSyncManager) DeleteFile(sessionID uint32) error {
	m.clear()
	pkt := protocol.BuildSyncFileDel(sessionID)
	fmt.Printf("  [FileSync] Deleting session_id=%d\n", sessionID)
	if err := m.encryptedWrite(pkt); err != nil {
		return fmt.Errorf("sending delete: %w", err)
	}
	return nil
}

func (m *FileSyncManager) ClearAllFiles() error {
	m.clear()
	pkt := protocol.BuildClearRecFile()
	fmt.Println("  [FileSync] Clearing all files...")
	if err := m.encryptedWrite(pkt); err != nil {
		return fmt.Errorf("sending clear: %w", err)
	}
	return nil
}

func DecryptAndSave(inputPath, outputPath string) error {
	return fmt.Errorf("use crypto.DecryptPLAUDContainer with the E2EE RSA key")
}
