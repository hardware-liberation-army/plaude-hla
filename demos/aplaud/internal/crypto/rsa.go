package crypto

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
)

func LoadRSAPrivateKey(pemData string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		key2, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parsing private key: %w (PKCS1: %w)", err, err2)
		}
		return key2, nil
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not RSA")
	}
	return rsaKey, nil
}

func LoadRSAPrivateKeyFromFile(path string) (*rsa.PrivateKey, error) {
	return nil, fmt.Errorf("not implemented: use LoadRSAPrivateKey with file contents")
}

func RSADecryptDeviceResponse(secretData []byte, privateKeyPEM string) ([]byte, error) {
	privateKey, err := LoadRSAPrivateKey(privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("loading RSA key: %w", err)
	}

	return RSADecryptPKCS1v15(secretData, privateKey)
}

func RSADecryptPKCS1v15(ciphertext []byte, privateKey *rsa.PrivateKey) ([]byte, error) {
	return rsa.DecryptPKCS1v15(nil, privateKey, ciphertext)
}
