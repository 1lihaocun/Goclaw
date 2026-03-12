package feishu

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const aesBlockSize = aes.BlockSize

var (
	ErrInvalidSignature      = errors.New("feishu: invalid request signature")
	ErrMissingSignature      = errors.New("feishu: missing signature headers")
	ErrInvalidEncryptedEvent = errors.New("feishu: invalid encrypted event")
)

func verifyRequestSignature(timestamp, nonce, encryptKey string, rawBody []byte, signature string) error {
	timestamp = strings.TrimSpace(timestamp)
	nonce = strings.TrimSpace(nonce)
	signature = strings.TrimSpace(strings.ToLower(signature))
	if timestamp == "" || nonce == "" || signature == "" {
		return ErrMissingSignature
	}

	sum := sha256.Sum256([]byte(timestamp + nonce + encryptKey + string(rawBody)))
	expected := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return ErrInvalidSignature
	}
	return nil
}

func decryptEncryptedPayload(encryptKey, encrypted string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return nil, ErrInvalidEncryptedEvent
	}
	if len(decoded) < aesBlockSize || len(decoded)%aesBlockSize != 0 {
		return nil, ErrInvalidEncryptedEvent
	}

	keyHash := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return nil, ErrInvalidEncryptedEvent
	}

	iv := decoded[:aesBlockSize]
	cipherText := append([]byte(nil), decoded[aesBlockSize:]...)
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(cipherText, cipherText)

	plainText, err := pkcs7Unpad(cipherText, aesBlockSize)
	if err != nil {
		return nil, ErrInvalidEncryptedEvent
	}
	return plainText, nil
}

func pkcs7Pad(value []byte, blockSize int) []byte {
	padding := blockSize - (len(value) % blockSize)
	if padding == 0 {
		padding = blockSize
	}
	return append(value, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func pkcs7Unpad(value []byte, blockSize int) ([]byte, error) {
	if len(value) == 0 || len(value)%blockSize != 0 {
		return nil, ErrInvalidEncryptedEvent
	}

	padding := int(value[len(value)-1])
	if padding == 0 || padding > blockSize || padding > len(value) {
		return nil, ErrInvalidEncryptedEvent
	}

	for _, b := range value[len(value)-padding:] {
		if int(b) != padding {
			return nil, ErrInvalidEncryptedEvent
		}
	}

	return value[:len(value)-padding], nil
}
