package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"testing"

	"golang.org/x/crypto/chacha20"
)

func TestDecryptPLAUDContainer(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	oggData := []byte("OggS" + " fake ogg audio data for testing purposes padding here")
	fileKey := make([]byte, 32)
	rand.Read(fileKey)
	uuid := make([]byte, 12)
	rand.Read(uuid)

	encryptedFileKey, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, fileKey)
	if err != nil {
		t.Fatal(err)
	}

	cipher, err := chacha20.NewUnauthenticatedCipher(fileKey, uuid)
	if err != nil {
		t.Fatal(err)
	}
	encryptedAudio := make([]byte, len(oggData))
	cipher.XORKeyStream(encryptedAudio, oggData)

	header := make([]byte, 0x200)
	copy(header[0:8], []byte("PLAUD.AI"))
	binary.LittleEndian.PutUint16(header[0x0A:], 0x0200)
	copy(header[0x84:0x90], uuid)
	copy(header[0x100:0x100+256], encryptedFileKey)

	container := make([]byte, 0, 0x200+len(encryptedAudio))
	container = append(container, header...)
	container = append(container, encryptedAudio...)

	decrypted, err := DecryptPLAUDContainer(container, key)
	if err != nil {
		t.Fatal("DecryptPLAUDContainer failed:", err)
	}

	if string(decrypted[:4]) != "OggS" {
		t.Fatalf("expected OggS header, got %x", decrypted[:4])
	}
	if string(decrypted) != string(oggData) {
		t.Fatalf("decrypted data mismatch:\n  got:  %x\n  want: %x", decrypted[:20], oggData[:20])
	}
}

func TestIsPLAUDContainer(t *testing.T) {
	tests := []struct {
		data []byte
		want bool
	}{
		{[]byte("PLAUD.AI"), true},
		{[]byte("OggS"), false},
		{[]byte("PLAUD"), false},
		{nil, false},
		{[]byte{}, false},
	}
	for i, tc := range tests {
		got := IsPLAUDContainer(tc.data)
		if got != tc.want {
			t.Errorf("test %d: IsPLAUDContainer(%v) = %v, want %v", i, tc.data, got, tc.want)
		}
	}
}
