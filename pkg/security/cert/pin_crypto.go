package cert

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"golang.org/x/crypto/pbkdf2"
)

const (
	pbkdf2Iterations = 600_000
	pbkdf2KeyLen     = 32 // AES-256
	saltLen          = 32
	nonceLen         = 12 // AES-GCM standard
	pemTypeEncrypted = "ENCRYPTED PRIVATE KEY"
)

var (
	ErrInvalidPIN = errors.New("invalid PIN: decryption failed")
	ErrInvalidKEK = errors.New("invalid KEK: decryption failed")
)

// EncryptKeyWithKEK encrypts an ECDSA private key with a server-side Key Encryption Key.
// Uses AES-256-GCM directly (no PBKDF2 — the KEK is already a proper key).
// The KEK must be exactly 32 bytes (hex-encoded 64 chars in env var).
func EncryptKeyWithKEK(key *ecdsa.PrivateKey, kek []byte) (string, error) {
	if len(kek) != 32 {
		return "", errors.New("KEK must be exactly 32 bytes")
	}

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("marshal PKCS#8: %w", err)
	}

	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, pkcs8Bytes, nil)

	// [12 bytes nonce][ciphertext+tag]
	payload := make([]byte, 0, nonceLen+len(ciphertext))
	payload = append(payload, nonce...)
	payload = append(payload, ciphertext...)

	pemBlock := &pem.Block{
		Type:  "KEK ENCRYPTED PRIVATE KEY",
		Bytes: payload,
	}

	return string(pem.EncodeToMemory(pemBlock)), nil
}

// DecryptKeyWithKEK decrypts a KEK-encrypted PEM private key.
func DecryptKeyWithKEK(encryptedPEM []byte, kek []byte) (*ecdsa.PrivateKey, error) {
	if len(kek) != 32 {
		return nil, errors.New("KEK must be exactly 32 bytes")
	}

	block, _ := pem.Decode(encryptedPEM)
	if block == nil {
		return nil, errors.New("no PEM data found")
	}

	payload := block.Bytes
	if len(payload) < nonceLen+aes.BlockSize {
		return nil, errors.New("encrypted data too short")
	}

	nonce := payload[:nonceLen]
	ciphertext := payload[nonceLen:]

	aesBlock, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(aesBlock)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	pkcs8Bytes, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrInvalidKEK
	}

	key, err := x509.ParsePKCS8PrivateKey(pkcs8Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS#8 key: %w", err)
	}

	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not ECDSA")
	}

	return ecKey, nil
}

// EncryptKeyWithPIN encrypts an ECDSA private key with a PIN using
// PBKDF2 (SHA-256, 600k iterations) + AES-256-GCM.
// Returns a PEM block with type "ENCRYPTED PRIVATE KEY" and headers
// containing salt and nonce (base64 in PEM headers).
func EncryptKeyWithPIN(key *ecdsa.PrivateKey, pin string) (string, error) {
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("marshal PKCS#8: %w", err)
	}

	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	derivedKey := pbkdf2.Key([]byte(pin), salt, pbkdf2Iterations, pbkdf2KeyLen, sha256.New)

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, pkcs8Bytes, nil)

	// Encode salt + nonce + ciphertext as a single blob:
	// [32 bytes salt][12 bytes nonce][ciphertext+tag]
	payload := make([]byte, 0, saltLen+nonceLen+len(ciphertext))
	payload = append(payload, salt...)
	payload = append(payload, nonce...)
	payload = append(payload, ciphertext...)

	pemBlock := &pem.Block{
		Type:  pemTypeEncrypted,
		Bytes: payload,
	}

	return string(pem.EncodeToMemory(pemBlock)), nil
}

// DecryptKeyWithPIN decrypts a PIN-encrypted PEM private key.
// Returns ErrInvalidPIN if the PIN is wrong (GCM authentication fails).
func DecryptKeyWithPIN(encryptedPEM []byte, pin string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(encryptedPEM)
	if block == nil {
		return nil, errors.New("no PEM data found")
	}

	payload := block.Bytes
	if len(payload) < saltLen+nonceLen+aes.BlockSize {
		return nil, errors.New("encrypted data too short")
	}

	salt := payload[:saltLen]
	nonce := payload[saltLen : saltLen+nonceLen]
	ciphertext := payload[saltLen+nonceLen:]

	derivedKey := pbkdf2.Key([]byte(pin), salt, pbkdf2Iterations, pbkdf2KeyLen, sha256.New)

	aesBlock, err := aes.NewCipher(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(aesBlock)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}

	pkcs8Bytes, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrInvalidPIN
	}

	key, err := x509.ParsePKCS8PrivateKey(pkcs8Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS#8 key: %w", err)
	}

	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not ECDSA")
	}

	return ecKey, nil
}
