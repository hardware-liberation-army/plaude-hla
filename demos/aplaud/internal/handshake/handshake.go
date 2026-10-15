package handshake

import (
	"encoding/base64"
	"fmt"
	"time"

	"aplaud/internal/ble"
	"aplaud/internal/config"
	"aplaud/internal/crypto"
	"aplaud/internal/protocol"
)

type Handshaker struct {
	transport *ble.Transport
	cfg       *config.Config
	session   *crypto.SessionCrypto
	notifyCh  chan []byte
}

type HandshakeResult struct {
	Session   *crypto.SessionCrypto
	DeviceVer uint16
}

func NewHandshaker(transport *ble.Transport, cfg *config.Config) *Handshaker {
	return &Handshaker{
		transport: transport,
		cfg:       cfg,
	}
}

func (h *Handshaker) Session() *crypto.SessionCrypto {
	return h.session
}

func (h *Handshaker) NotifyCh() <-chan []byte {
	return h.notifyCh
}

func (h *Handshaker) DoHandshake() (*HandshakeResult, error) {
	if err := h.transport.DiscoverServices(); err != nil {
		return nil, fmt.Errorf("discovering services: %w", err)
	}

	notifyCh := make(chan []byte, 256)
	h.notifyCh = notifyCh
	err := h.transport.SubscribeNotify(func(data []byte) {
		select {
		case notifyCh <- data:
		default:
		}
	})
	if err != nil {
		return nil, fmt.Errorf("subscribing to notifications: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	fmt.Println("\n=== Phase 1: SN Signature (0xFE10) — unencrypted ===")
	sigBytes, err := h.cfg.GetSNSignatureBytes()
	if err != nil {
		return nil, fmt.Errorf("getting SN signature: %w", err)
	}

	snPackets := protocol.BuildSNSignaturePackets(sigBytes)
	for i, pkt := range snPackets {
		fmt.Printf("  Sending SN sig chunk %d/%d (%dB)\n", i+1, len(snPackets), len(pkt))
		if err := h.transport.WriteCommand(pkt); err != nil {
			return nil, fmt.Errorf("sending SN sig chunk %d: %w", i, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Println("  Waiting for 0xFE12 response...")
	var fe12Chunks [][]byte
	timeout := time.After(5 * time.Second)
WAIT_FE12:
	for {
		select {
		case data := <-notifyCh:
			fe12Chunks = append(fe12Chunks, data)
		case <-timeout:
			break WAIT_FE12
		}
	}

	if len(fe12Chunks) == 0 {
		return nil, fmt.Errorf("no 0xFE12 response received")
	}
	fmt.Printf("  Got %d notification(s)\n", len(fe12Chunks))

	fmt.Println("\n=== Phase 2: Key Derivation ===")
	secretData := protocol.ReassembleChunks(fe12Chunks, 0xFE12)
	if secretData == nil {
		return nil, fmt.Errorf("failed to reassemble 0xFE12 chunks")
	}
	fmt.Printf("  Reassembled secret data: %dB\n", len(secretData))

	rsaKey, err := h.cfg.GetRSAPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("loading RSA key: %w", err)
	}

	decrypted, err := crypto.RSADecryptPKCS1v15(secretData, rsaKey)
	if err != nil {
		return nil, fmt.Errorf("RSA decryption failed: %w", err)
	}
	fmt.Printf("  RSA decrypted (%dB)\n", len(decrypted))

	if len(decrypted) < 56 {
		return nil, fmt.Errorf("decrypted data too short: %d bytes (need 56+)", len(decrypted))
	}

	session, err := crypto.DeriveFromRSAResponse(decrypted)
	if err != nil {
		return nil, fmt.Errorf("deriving session keys: %w", err)
	}
	h.session = session
	fmt.Printf("  Crypto initialized: %s\n", session)

	if len(decrypted) >= 80 {
		verified := session.Verify(decrypted[56:80])
		fmt.Printf("  Verification: %v\n", map[bool]string{true: "PASSED", false: "FAILED"}[verified])
		if !verified {
			return nil, fmt.Errorf("session verification failed")
		}
	}

	fmt.Println("\n=== Phase 3: Encrypted Handshake (cmd 1) ===")
	jwtSub, err := h.cfg.GetJWTSubject()
	if err != nil {
		return nil, fmt.Errorf("getting JWT subject: %w", err)
	}
	fmt.Printf("  Using JWT sub as bind token: %s\n", jwtSub)

	hsData := protocol.BuildHandshake(jwtSub, 0, 20)
	encryptedHS := session.EncryptSend(hsData)
	if err := h.transport.WriteCommand(encryptedHS); err != nil {
		return nil, fmt.Errorf("sending encrypted handshake: %w", err)
	}

	time.Sleep(3 * time.Second)

	fmt.Println("\n=== Phase 4: Post-handshake init sequence ===")
	initSeq := protocol.GetInitSequence()
	for _, step := range initSeq {
		data := step.Builder()
		encrypted := session.EncryptSend(data)
		if err := h.transport.WriteCommand(encrypted); err != nil {
			fmt.Printf("  Warning: %s failed: %v\n", step.Label, err)
		}
		time.Sleep(150 * time.Millisecond)
	}

	time.Sleep(1 * time.Second)

	fmt.Println("\nHandshake complete.")
	return &HandshakeResult{Session: session}, nil
}

func (h *Handshaker) EncryptedWrite(data []byte) error {
	if h.session == nil {
		return fmt.Errorf("no session established")
	}
	encrypted := h.session.EncryptSend(data)
	return h.transport.WriteCommand(encrypted)
}

func BuildSNSignaturePackets(b64sig string) ([][]byte, error) {
	sigBytes, err := base64.StdEncoding.DecodeString(b64sig)
	if err != nil {
		return nil, fmt.Errorf("decoding SN signature: %w", err)
	}
	return protocol.BuildSNSignaturePackets(sigBytes), nil
}
