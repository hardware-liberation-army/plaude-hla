package crypto

import (
	"crypto/rsa"
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/chacha20"
)

const (
	headerMagic       = "PLAUD.AI"
	headerSizeOffset  = 0x0A
	uuidOffset        = 0x84
	uuidSize          = 12
	encryptedKeyOff   = 0x100
	encryptedKeySize  = 256
)

func DecryptPLAUDContainer(data []byte, rsaKey *rsa.PrivateKey) ([]byte, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("file too short: %d bytes", len(data))
	}
	if string(data[:8]) != headerMagic {
		return nil, fmt.Errorf("not a PLAUD.AI container (magic: %q)", string(data[:8]))
	}

	headerSize := binary.LittleEndian.Uint16(data[headerSizeOffset : headerSizeOffset+2])
	uuidBytes := data[uuidOffset : uuidOffset+uuidSize]
	encryptedKeyBlock := data[encryptedKeyOff : encryptedKeyOff+encryptedKeySize]

	fileKey, err := RSADecryptPKCS1v15(encryptedKeyBlock, rsaKey)
	if err != nil {
		return nil, fmt.Errorf("RSA decrypt file key: %w", err)
	}
	if len(fileKey) != 32 {
		return nil, fmt.Errorf("expected 32-byte file key, got %d", len(fileKey))
	}

	nonce := make([]byte, 12)
	copy(nonce, uuidBytes)

	cipher, err := chacha20.NewUnauthenticatedCipher(fileKey, nonce)
	if err != nil {
		return nil, fmt.Errorf("creating ChaCha20 cipher: %w", err)
	}

	audioData := make([]byte, len(data)-int(headerSize))
	cipher.XORKeyStream(audioData, data[headerSize:])

	if len(audioData) < 4 || string(audioData[:4]) != "OggS" {
		return nil, fmt.Errorf("decryption failed: no OGG header found (first 4 bytes: %x)", audioData[:min(4, len(audioData))])
	}

	return audioData, nil
}

func IsPLAUDContainer(data []byte) bool {
	return len(data) >= 8 && string(data[:8]) == headerMagic
}
