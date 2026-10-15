package tui

import (
	"crypto/rsa"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"aplaud/internal/ble"
	"aplaud/internal/config"
	"aplaud/internal/crypto"
	"aplaud/internal/filesync"
	"aplaud/internal/handshake"
	"aplaud/internal/protocol"
	"aplaud/internal/whisper"
	tea "charm.land/bubbletea/v2"
)

const (
	StatusDisconnected  = "disconnected"
	StatusScanning      = "scanning"
	StatusConnecting    = "connecting"
	StatusHandshaking   = "handshaking"
	StatusConnected     = "connected"
	StatusMonitoring    = "monitoring"
	StatusReconnecting  = "reconnecting"
	StatusError         = "error"
)

type BLEBridge struct {
	mu           sync.Mutex
	cfg          *config.Config
	status       string
	addr         string
	deviceName   string
	battery      int
	lastSync     time.Time
	err          error
	syncPhase    string
	syncCurrent  int
	syncTotal    int
	syncSession  uint32

	transport    *ble.Transport
	handshaker   *handshake.Handshaker
	fs           *filesync.FileSyncManager
	notifyDone   chan struct{}

	send              func(tea.Msg)
	pollInterval      time.Duration
	autoSync          bool
	autoReconnect     bool
	stopCh            chan struct{}
	consecutiveErrors int

	transcriber  *whisper.Transcriber
	e2eeKey      *rsa.PrivateKey
}

func NewBLEBridge(cfg *config.Config, send func(tea.Msg)) *BLEBridge {
	pi := time.Duration(cfg.TUI.PollInterval) * time.Second
	if pi < 10*time.Second {
		pi = 300 * time.Second
	}

	var tr *whisper.Transcriber
	var e2eeKey *rsa.PrivateKey

	if cfg.Whisper.WhisperCliPath != "" && cfg.Whisper.ModelPath != "" {
		tr = whisper.NewTranscriber(
			cfg.Whisper.WhisperCliPath,
			cfg.Whisper.ModelPath,
			cfg.Whisper.Language,
			cfg.Whisper.LibPath,
			cfg.Whisper.GGMLLibPath,
			cfg.Whisper.VAD,
			cfg.Whisper.VADModelPath,
			cfg.Whisper.VADThreshold,
		)
	}

	key, err := cfg.GetE2EERSAPrivateKey()
	if err == nil {
		e2eeKey = key
	}

	return &BLEBridge{
		cfg:           cfg,
		status:        StatusDisconnected,
		send:          send,
		pollInterval:  pi,
		autoSync:      cfg.TUI.AutoSync,
		autoReconnect: cfg.TUI.AutoReconnect,
		stopCh:        make(chan struct{}),
		transcriber:   tr,
		e2eeKey:       e2eeKey,
	}
}

func (b *BLEBridge) Status() (string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status, b.addr
}

func (b *BLEBridge) Battery() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.battery
}

func (b *BLEBridge) LastSync() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastSync
}

func (b *BLEBridge) SyncProgress() (string, int, int, uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.syncPhase, b.syncCurrent, b.syncTotal, b.syncSession
}

func (b *BLEBridge) PollInterval() time.Duration {
	return b.pollInterval
}

func (b *BLEBridge) SetAutoSync(on bool) {
	b.mu.Lock()
	b.autoSync = on
	b.mu.Unlock()
}

func (b *BLEBridge) AutoSync() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.autoSync
}

func (b *BLEBridge) IsSyncing() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.syncPhase != ""
}

func (b *BLEBridge) sendMsg(msg tea.Msg) {
	if b.send != nil {
		b.send(msg)
	}
}

func (b *BLEBridge) setStatus(status string) {
	b.mu.Lock()
	b.status = status
	b.mu.Unlock()
	b.sendMsg(ConnectionStatusMsg{Status: status, Addr: b.addr})
}

func (b *BLEBridge) setSyncProgress(phase string, current, total int, sessionID uint32) {
	b.mu.Lock()
	b.syncPhase = phase
	b.syncCurrent = current
	b.syncTotal = total
	b.syncSession = sessionID
	b.mu.Unlock()
}

func (b *BLEBridge) clearSyncProgress() {
	b.mu.Lock()
	b.syncPhase = ""
	b.syncCurrent = 0
	b.syncTotal = 0
	b.syncSession = 0
	b.mu.Unlock()
}

var GlobalLogCapture = NewLogCapture(2000)

func (b *BLEBridge) Connect() {
	go func() {
		if err := b.doConnect(); err != nil {
			return
		}
		if b.autoSync {
			b.startMonitoring()
		}
	}()
}

func (b *BLEBridge) doConnect() error {
	b.setStatus(StatusScanning)

	transport := ble.NewTransport()
	b.mu.Lock()
	b.transport = transport
	b.mu.Unlock()

	var devices []ble.DeviceInfo
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		devices, err = ble.Scan(b.cfg.BLEScanDuration)
		if err == nil {
			break
		}
		b.sendMsg(ErrorMsg{Err: fmt.Errorf("BLE scan attempt %d failed: %w", attempt+1, err)})
	}
	if err != nil {
		b.mu.Lock()
		b.err = err
		b.mu.Unlock()
		b.setStatus(StatusError)
		b.sendMsg(ErrorMsg{Err: err})
		return err
	}

	if len(devices) == 0 {
		err := fmt.Errorf("no Plaud devices found")
		b.mu.Lock()
		b.err = err
		b.mu.Unlock()
		b.setStatus(StatusError)
		b.sendMsg(ErrorMsg{Err: err})
		return err
	}

	b.mu.Lock()
	knownName := b.deviceName
	staleAddr := b.addr
	b.mu.Unlock()

	devices = b.prioritizeDevices(devices, knownName, staleAddr)

	if staleAddr != "" && len(devices) > 0 && devices[0].Address.String() != staleAddr {
		b.sendMsg(ErrorMsg{Err: fmt.Errorf("device MAC changed: %s → %s (name: %s)", staleAddr, devices[0].Address.String(), devices[0].Name)})
	}

	var hs *handshake.Handshaker
	var result *handshake.HandshakeResult
	var lastErr error

	for _, dev := range devices {
		addr := dev.Address.String()
		name := dev.Name

		b.mu.Lock()
		b.addr = addr
		b.deviceName = name
		b.mu.Unlock()

		b.setStatus(StatusConnecting)

		connectCh := make(chan error, 1)
		go func() {
			connectCh <- transport.Connect(addr)
		}()

		select {
		case connectErr := <-connectCh:
			if connectErr != nil {
				lastErr = fmt.Errorf("connect to %s: %w", addr, connectErr)
				b.sendMsg(ErrorMsg{Err: lastErr})
				continue
			}
		case <-time.After(30 * time.Second):
			lastErr = fmt.Errorf("BLE connect to %s timed out after 30s", addr)
			b.sendMsg(ErrorMsg{Err: lastErr})
			continue
		}

		b.setStatus(StatusHandshaking)
		hs = handshake.NewHandshaker(transport, b.cfg)
		result, lastErr = hs.DoHandshake()
		if lastErr != nil {
			lastErr = fmt.Errorf("handshake with %s: %w", addr, lastErr)
			transport.Disconnect()
			b.sendMsg(ErrorMsg{Err: lastErr})
			continue
		}

		lastErr = nil
		break
	}

	if lastErr != nil {
		b.mu.Lock()
		b.err = lastErr
		b.mu.Unlock()
		b.setStatus(StatusError)
		b.sendMsg(ErrorMsg{Err: lastErr})
		return lastErr
	}

	b.mu.Lock()
	b.handshaker = hs
	b.err = nil
	b.mu.Unlock()

	fs := filesync.NewFileSyncManager(
		func(data []byte) ([]byte, error) {
			return result.Session.DecryptRecv(data)
		},
		func(data []byte) error {
			return hs.EncryptedWrite(data)
		},
	)

	b.mu.Lock()
	b.fs = fs
	b.notifyDone = make(chan struct{})
	b.mu.Unlock()

	go func() {
		for data := range hs.NotifyCh() {
			b.mu.Lock()
			done := b.notifyDone
			b.mu.Unlock()
			select {
			case <-done:
				return
			default:
			}
			fs.HandleNotification(data)
		}
	}()

	b.consecutiveErrors = 0
	b.setStatus(StatusConnected)
	b.sendMsg(ConnectionStatusMsg{Status: StatusConnected, Addr: b.addr})
	return nil
}

func (b *BLEBridge) prioritizeDevices(devices []ble.DeviceInfo, knownName, staleAddr string) []ble.DeviceInfo {
	if knownName == "" && staleAddr == "" {
		return devices
	}

	var preferred, other []ble.DeviceInfo
	for _, d := range devices {
		addr := d.Address.String()
		isStale := addr == staleAddr
		nameMatch := knownName != "" && d.Name == knownName

		if isStale && nameMatch {
			continue
		}
		if isStale {
			continue
		}
		if nameMatch {
			preferred = append(preferred, d)
		} else {
			other = append(other, d)
		}
	}

	result := make([]ble.DeviceInfo, 0, len(devices))
	result = append(result, preferred...)
	result = append(result, other...)
	return result
}

func (b *BLEBridge) startMonitoring() {
	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	b.setStatus(StatusMonitoring)
	b.doPoll()

	for {
		select {
		case <-b.stopCh:
			return
		case <-ticker.C:
			if !b.autoSync {
				continue
			}
			if err := b.doPoll(); err != nil {
				b.consecutiveErrors++
				if b.consecutiveErrors >= 3 {
					b.cleanupConnection()
					if b.autoReconnect {
						if b.reconnectLoop() {
							b.startMonitoring()
						}
					}
					return
				}
			} else {
				b.consecutiveErrors = 0
			}
		}
	}
}

func (b *BLEBridge) reconnectLoop() bool {
	backoff := 10 * time.Second

	for {
		b.setStatus(StatusReconnecting)
		b.sendMsg(ErrorMsg{Err: fmt.Errorf("device disconnected — reconnecting in %v", backoff)})

		select {
		case <-b.stopCh:
			return false
		case <-time.After(backoff):
		}

		ble.StopScan()
		time.Sleep(2 * time.Second)

		if err := b.doConnect(); err != nil {
			backoff = backoff * 2
			if backoff > 300*time.Second {
				backoff = 300 * time.Second
			}
			continue
		}

		return true
	}
}

func (b *BLEBridge) cleanupConnection() {
	b.mu.Lock()
	if b.notifyDone != nil {
		close(b.notifyDone)
		b.notifyDone = nil
	}
	if b.transport != nil {
		b.transport.Disconnect()
	}
	b.transport = nil
	b.fs = nil
	b.handshaker = nil
	b.mu.Unlock()
}

func (b *BLEBridge) doPoll() error {
	b.mu.Lock()
	fs := b.fs
	b.mu.Unlock()

	if fs == nil {
		return fmt.Errorf("not connected")
	}

	b.setSyncProgress("listing", 0, 0, 0)
	b.sendMsg(SyncStartMsg{})

	fileList, err := fs.GetFileList(0, 15*time.Second)
	if err != nil {
		b.clearSyncProgress()
		b.sendMsg(ErrorMsg{Err: fmt.Errorf("file list poll: %w", err)})
		return err
	}

	var newFiles []protocol.BleFile
	var toRetranscribe []uint32
	var toDelete []uint32

	for _, f := range fileList.Files {
		txtPath := filepath.Join(b.cfg.DownloadDir, fmt.Sprintf("recording_%d.txt", f.SessionID))
		oggPath := filepath.Join(b.cfg.DownloadDir, fmt.Sprintf("recording_%d.ogg", f.SessionID))

		if fileExists(txtPath) {
			toDelete = append(toDelete, f.SessionID)
			continue
		}
		if fileExists(oggPath) {
			toRetranscribe = append(toRetranscribe, f.SessionID)
			continue
		}
		newFiles = append(newFiles, protocol.BleFile{SessionID: f.SessionID, FileSize: f.FileSize})
	}

	for _, sid := range toDelete {
		if err := fs.DeleteFile(sid); err != nil {
			b.sendMsg(ErrorMsg{Err: fmt.Errorf("delete %d: %w", sid, err)})
		}
	}

	totalWork := len(toRetranscribe) + len(newFiles)
	if totalWork == 0 && len(toDelete) == 0 {
		b.mu.Lock()
		b.lastSync = time.Now()
		b.mu.Unlock()
		b.clearSyncProgress()
		b.sendMsg(SyncDoneMsg{})
		return nil
	}

	b.setSyncProgress("processing", 0, totalWork, 0)
	b.sendMsg(SyncProgressMsg{Phase: "processing", Total: totalWork})

	completed := 0
	var allNew []uint32

	for _, sid := range toRetranscribe {
		completed++
		oggPath := filepath.Join(b.cfg.DownloadDir, fmt.Sprintf("recording_%d.ogg", sid))

		b.setSyncProgress("transcribing", completed, totalWork, sid)
		b.sendMsg(SyncProgressMsg{
			Phase:     "transcribing",
			Current:   completed,
			Total:     totalWork,
			SessionID: sid,
		})

		if b.transcriber != nil {
			result, err := b.transcriber.Transcribe(oggPath)
			if err != nil {
				b.sendMsg(ErrorMsg{Err: fmt.Errorf("transcribe %d: %w", sid, err)})
				continue
			}
			ts := time.Unix(int64(sid), 0)
			b.sendMsg(NewTranscriptionMsg{
				SessionID: sid,
				Text:      result.Text,
				Time:      ts,
			})
			allNew = append(allNew, sid)
		}

		if err := fs.DeleteFile(sid); err != nil {
			b.sendMsg(ErrorMsg{Err: fmt.Errorf("delete %d: %w", sid, err)})
		}
	}

	for _, f := range newFiles {
		completed++
		sid := f.SessionID
		fileSize := f.FileSize

		binPath := filepath.Join(b.cfg.DownloadDir, fmt.Sprintf("recording_%d.bin", sid))
		oggPath := filepath.Join(b.cfg.DownloadDir, fmt.Sprintf("recording_%d.ogg", sid))

		b.setSyncProgress("downloading", completed, totalWork, sid)
		b.sendMsg(SyncProgressMsg{
			Phase:     "downloading",
			Current:   completed,
			Total:     totalWork,
			SessionID: sid,
		})

		_, err := fs.DownloadFile(sid, fileSize, binPath)
		if err != nil {
			b.sendMsg(ErrorMsg{Err: fmt.Errorf("download %d: %w", sid, err)})
			continue
		}

		if b.e2eeKey != nil {
			if err := b.decryptFile(binPath, oggPath); err != nil {
				b.sendMsg(ErrorMsg{Err: fmt.Errorf("decrypt %d: %w", sid, err)})
				os.Remove(binPath)
				continue
			}
			os.Remove(binPath)
		}

		if b.transcriber != nil && b.e2eeKey != nil {
			b.setSyncProgress("transcribing", completed, totalWork, sid)
			b.sendMsg(SyncProgressMsg{
				Phase:     "transcribing",
				Current:   completed,
				Total:     totalWork,
				SessionID: sid,
			})

			result, err := b.transcriber.Transcribe(oggPath)
			if err != nil {
				b.sendMsg(ErrorMsg{Err: fmt.Errorf("transcribe %d: %w", sid, err)})
				continue
			}
			ts := time.Unix(int64(sid), 0)
			b.sendMsg(NewTranscriptionMsg{
				SessionID: sid,
				Text:      result.Text,
				Time:      ts,
			})
		}

		allNew = append(allNew, sid)

		if err := fs.DeleteFile(sid); err != nil {
			b.sendMsg(ErrorMsg{Err: fmt.Errorf("delete %d: %w", sid, err)})
		}
	}

	b.mu.Lock()
	b.lastSync = time.Now()
	b.mu.Unlock()
	b.clearSyncProgress()
	b.sendMsg(SyncDoneMsg{NewFiles: allNew})
	return nil
}

func (b *BLEBridge) decryptFile(inputPath, outputPath string) error {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	if !crypto.IsPLAUDContainer(data) {
		return fmt.Errorf("not a PLAUD.AI container")
	}

	audio, err := crypto.DecryptPLAUDContainer(data, b.e2eeKey)
	if err != nil {
		return fmt.Errorf("decrypting: %w", err)
	}

	return os.WriteFile(outputPath, audio, 0644)
}

func (b *BLEBridge) SyncNow() {
	go b.doPoll()
}

func (b *BLEBridge) Disconnect() {
	close(b.stopCh)
	b.mu.Lock()
	if b.notifyDone != nil {
		close(b.notifyDone)
		b.notifyDone = nil
	}
	if b.transport != nil {
		b.transport.Disconnect()
	}
	b.transport = nil
	b.fs = nil
	b.handshaker = nil
	b.mu.Unlock()
	b.setStatus(StatusDisconnected)
}

func (b *BLEBridge) Stop() {
	b.Disconnect()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
