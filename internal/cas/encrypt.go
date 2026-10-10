package cas

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

const randomAlphabet = "ABCDEFGHJKMNPQRSTWXYZabcdefhijkmnprstwxyz2345678"

// encryptPassword matches authserver's encrypt.js: a 64-character random
// prefix, the password, and an independent 16-character IV. The IV is not
// transmitted; the server discards the random prefix when decrypting.
func encryptPassword(password, salt string, random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	salt = strings.TrimSpace(salt)
	if len(salt) != aes.BlockSize {
		return "", fmt.Errorf("salt must be 16 bytes")
	}
	prefix, err := randomString(64, random)
	if err != nil {
		return "", err
	}
	iv, err := randomString(aes.BlockSize, random)
	if err != nil {
		return "", err
	}
	return encryptAES([]byte(prefix+password), []byte(salt), []byte(iv))
}

func randomString(n int, random io.Reader) (string, error) {
	out := make([]byte, n)
	var b [1]byte
	max := 256 - (256 % len(randomAlphabet))
	for i := 0; i < len(out); {
		if _, err := io.ReadFull(random, b[:]); err != nil {
			return "", err
		}
		if int(b[0]) >= max {
			continue
		}
		out[i] = randomAlphabet[int(b[0])%len(randomAlphabet)]
		i++
	}
	return string(out), nil
}

func encryptAES(plain, key, iv []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(iv) != block.BlockSize() {
		return "", fmt.Errorf("IV must be 16 bytes")
	}
	pad := block.BlockSize() - len(plain)%block.BlockSize()
	padded := make([]byte, len(plain)+pad)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(padded, padded)
	return base64.StdEncoding.EncodeToString(padded), nil
}
