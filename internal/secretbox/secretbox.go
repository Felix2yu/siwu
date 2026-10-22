// Package secretbox 用本机密钥加密落库的敏感串（通知 URL 里的 token）。
// 密钥文件由首次启动时生成，权限 0600，不进版本库。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const keyLen = 32

// Box 是 AES-256-GCM 的封装。
type Box struct {
	aead cipher.AEAD
}

// LoadOrCreate 从 path 读密钥，不存在则生成。
func LoadOrCreate(path string) (*Box, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, keyLen)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
			return nil, err
		}
		return newBox(key)
	}
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil || len(key) != keyLen {
		return nil, fmt.Errorf("invalid secret key at %s", path)
	}
	return newBox(key)
}

func newBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal 加密。
func (b *Box) Seal(plain string) ([]byte, error) {
	if plain == "" {
		return nil, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

// Open 解密。
func (b *Box) Open(sealed []byte) (string, error) {
	if len(sealed) == 0 {
		return "", nil
	}
	ns := b.aead.NonceSize()
	if len(sealed) < ns {
		return "", errors.New("ciphertext too short")
	}
	plain, err := b.aead.Open(nil, sealed[:ns], sealed[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
