package crypto

import (
	"crypto/cipher"
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

var plaudVerification = []byte("PLAUD.AI")

type SessionCrypto struct {
	key         [32]byte
	nonce       [12]byte
	aad         []byte
	aead        cipher.AEAD
	sendCounter uint32
}

func NewSessionCrypto(key []byte, nonce []byte, aad []byte) (*SessionCrypto, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	if len(nonce) != 12 {
		return nil, fmt.Errorf("nonce must be 12 bytes, got %d", len(nonce))
	}

	sc := &SessionCrypto{
		sendCounter: 1,
	}
	copy(sc.key[:], key)
	copy(sc.nonce[:], nonce)
	if len(aad) > 0 {
		sc.aad = make([]byte, len(aad))
		copy(sc.aad, aad)
	}

	aead, err := chacha20poly1305.New(sc.key[:])
	if err != nil {
		return nil, fmt.Errorf("creating AEAD: %w", err)
	}
	sc.aead = aead

	return sc, nil
}

func DeriveFromRSAResponse(decrypted []byte) (*SessionCrypto, error) {
	if len(decrypted) < 56 {
		return nil, fmt.Errorf("need at least 56 bytes, got %d", len(decrypted))
	}
	key := decrypted[0:32]
	nonce := decrypted[32:44]
	aad := decrypted[44:56]
	return NewSessionCrypto(key, nonce, aad)
}

func (sc *SessionCrypto) Verify(verificationCT []byte) bool {
	plaintext, err := sc.aead.Open(nil, sc.nonce[:], verificationCT, sc.aad)
	if err != nil {
		return false
	}
	return string(plaintext) == string(plaudVerification)
}

func (sc *SessionCrypto) EncryptSend(plaintext []byte) []byte {
	sc.sendCounter++
	counterBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(counterBytes, sc.sendCounter)
	dataWithCounter := append(counterBytes, plaintext...)
	return sc.aead.Seal(nil, sc.nonce[:], dataWithCounter, sc.aad)
}

func (sc *SessionCrypto) DecryptRecv(ciphertext []byte) ([]byte, error) {
	decrypted, err := sc.aead.Open(nil, sc.nonce[:], ciphertext, sc.aad)
	if err != nil {
		return nil, fmt.Errorf("AEAD decrypt: %w", err)
	}
	if len(decrypted) < 4 {
		return decrypted, nil
	}
	return decrypted[4:], nil
}

func (sc *SessionCrypto) SendCounter() uint32 {
	return sc.sendCounter
}

func (sc *SessionCrypto) SetSendCounter(v uint32) {
	sc.sendCounter = v
}

func (sc *SessionCrypto) Key() []byte {
	return sc.key[:]
}

func (sc *SessionCrypto) Nonce() []byte {
	return sc.nonce[:]
}

func (sc *SessionCrypto) AAD() []byte {
	return sc.aad
}

func (sc *SessionCrypto) String() string {
	return fmt.Sprintf("SessionCrypto(key=%x..., nonce=%x, counter=%d)",
		sc.key[:8], sc.nonce, sc.sendCounter)
}
